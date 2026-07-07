// Package awsconsole is fab's AWS Console fixture engine. It runs Moto
// server mode in Docker and seeds it through AWS APIs, giving agents a
// stateful AWS-compatible endpoint without touching a real cloud account.
package awsconsole

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/dumbmachine/fabricate/engine"
	"github.com/dumbmachine/fabricate/profile"
	"github.com/testcontainers/testcontainers-go"
	tcwait "github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"
)

const (
	// Engine is the slug profiles set in `engine:` and the
	// cli-layer registry key for this engine.
	Engine = "aws_console"

	defaultImage       = "motoserver/moto:latest"
	defaultPort        = "5000/tcp"
	defaultRegion      = "us-east-1"
	defaultAccountID   = "123456789012"
	defaultAccessKeyID = "AKIAFABMOTO"
	defaultSecretKey   = "fab-moto-secret"
	extraAccountID     = "account_id"
	extraRegion        = "region"
	extraEndpointURL   = "endpoint_url"
)

// New returns an AWS Console/Moto engine.
func New() engine.Engine { return &eng{} }

type eng struct{}

func (eng) Info() engine.Info {
	return engine.Info{
		Slug:           Engine,
		DefaultImage:   defaultImage,
		DefaultPort:    5000,
		SupportedSeeds: []string{profile.SeedTypeMotoSeed},
		Description:    "Stateful AWS API emulator (Moto server mode). Returns endpoint URL plus fake AWS credentials. moto-seed applies S3, IAM, EC2/NAT, and ELBv2 fixtures for audit automations.",
	}
}

func (eng) Create(ctx context.Context, name string, p *profile.Profile) (*engine.Instance, error) {
	image := engine.OrDefault(p.Image, defaultImage)
	seeds, err := loadSeedDocs(p)
	if err != nil {
		return nil, fmt.Errorf("aws_console engine: load seeds: %w", err)
	}

	region := firstNonEmpty(p.Env["AWS_REGION"], p.Env["AWS_DEFAULT_REGION"], firstSeedRegion(seeds), defaultRegion)
	accountID := firstNonEmpty(p.Env["AWS_ACCOUNT_ID"], firstSeedAccountID(seeds), defaultAccountID)
	accessKeyID := engine.OrDefault(p.Defaults.Username, defaultAccessKeyID)
	secretKey := engine.OrDefault(p.Defaults.Password, defaultSecretKey)

	t := engine.ParseTimeout(p.Healthcheck.Timeout, 60*time.Second)
	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{defaultPort},
		Env: map[string]string{
			"AWS_DEFAULT_REGION": region,
			"MOTO_ACCOUNT_ID":    accountID,
		},
		WaitingFor: tcwait.ForListeningPort(defaultPort).WithStartupTimeout(t),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		return nil, fmt.Errorf("aws_console engine: container start: %w (image=%s)", err, image)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("aws_console engine: host: %w", err)
	}
	mapped, err := c.MappedPort(ctx, defaultPort)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("aws_console engine: port: %w", err)
	}
	port := mapped.Int()
	url := fmt.Sprintf("http://%s:%d", host, port)

	if err := applySeedDocs(ctx, url, region, accessKeyID, secretKey, seeds); err != nil {
		_ = c.Terminate(context.Background())
		return nil, fmt.Errorf("aws_console engine: seed: %w", err)
	}

	return &engine.Instance{
		Name:        name,
		Profile:     p.Name,
		Engine:      Engine,
		Image:       image,
		ContainerID: c.GetContainerID(),
		Creds: engine.Creds{
			Engine:   Engine,
			Host:     host,
			Port:     port,
			Username: accessKeyID,
			Password: secretKey,
			Database: region,
			URL:      url,
			Extra: map[string]string{
				extraAccountID:   accountID,
				extraRegion:      region,
				extraEndpointURL: url,
			},
		},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func (eng) Destroy(ctx context.Context, containerID string) error {
	return engine.DockerRM(ctx, containerID)
}

type seedDoc struct {
	AccountID string    `yaml:"account_id,omitempty"`
	Region    string    `yaml:"region,omitempty"`
	S3        s3Seed    `yaml:"s3,omitempty"`
	IAM       iamSeed   `yaml:"iam,omitempty"`
	EC2       ec2Seed   `yaml:"ec2,omitempty"`
	ELBV2     elbv2Seed `yaml:"elbv2,omitempty"`
}

type s3Seed struct {
	Buckets []s3Bucket `yaml:"buckets,omitempty"`
}

type s3Bucket struct {
	Name              string             `yaml:"name"`
	PublicAccessBlock *publicAccessBlock `yaml:"public_access_block,omitempty"`
	Policy            any                `yaml:"policy,omitempty"`
	Encryption        bool               `yaml:"encryption,omitempty"`
	Objects           []s3Object         `yaml:"objects,omitempty"`
}

type publicAccessBlock struct {
	BlockPublicACLs       bool `yaml:"block_public_acls"`
	IgnorePublicACLs      bool `yaml:"ignore_public_acls"`
	BlockPublicPolicy     bool `yaml:"block_public_policy"`
	RestrictPublicBuckets bool `yaml:"restrict_public_buckets"`
}

type s3Object struct {
	Key         string `yaml:"key"`
	Body        string `yaml:"body,omitempty"`
	BodyBase64  string `yaml:"body_base64,omitempty"`
	ContentType string `yaml:"content_type,omitempty"`
}

func loadSeedDocs(p *profile.Profile) ([]seedDoc, error) {
	docs := make([]seedDoc, 0, len(p.Seed))
	for i, step := range p.Seed {
		if step.Type != profile.SeedTypeMotoSeed {
			return nil, fmt.Errorf("seed step %d type=%q not supported (%s)", i, step.Type, profile.SeedTypeMotoSeed)
		}
		raw, err := p.ReadSeed(step)
		if err != nil {
			return nil, fmt.Errorf("read seed %q: %w", step.File, err)
		}
		var doc seedDoc
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse seed %q: %w", step.File, err)
		}
		if err := doc.validate(step.File); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func (d seedDoc) validate(file string) error {
	seenBuckets := map[string]bool{}
	for i, b := range d.S3.Buckets {
		if strings.TrimSpace(b.Name) == "" {
			return fmt.Errorf("seed %q: s3.buckets[%d].name is required", file, i)
		}
		if seenBuckets[b.Name] {
			return fmt.Errorf("seed %q: duplicate s3 bucket %q", file, b.Name)
		}
		seenBuckets[b.Name] = true
		seenObjects := map[string]bool{}
		for j, obj := range b.Objects {
			if strings.TrimSpace(obj.Key) == "" {
				return fmt.Errorf("seed %q: s3.buckets[%d].objects[%d].key is required", file, i, j)
			}
			if obj.Body != "" && obj.BodyBase64 != "" {
				return fmt.Errorf("seed %q: s3.buckets[%d].objects[%d] must use body or body_base64, not both", file, i, j)
			}
			if seenObjects[obj.Key] {
				return fmt.Errorf("seed %q: duplicate object %q in bucket %q", file, obj.Key, b.Name)
			}
			seenObjects[obj.Key] = true
		}
	}
	if err := validateIAMSeed(file, d.IAM); err != nil {
		return err
	}
	if err := validateEC2Seed(file, d.EC2); err != nil {
		return err
	}
	if err := validateELBV2Seed(file, d.ELBV2); err != nil {
		return err
	}
	return nil
}

func applySeedDocs(ctx context.Context, endpointURL, defaultRegion, accessKeyID, secretKey string, docs []seedDoc) error {
	for i, doc := range docs {
		region := firstNonEmpty(doc.Region, defaultRegion, defaultRegion)
		cfg, err := newAWSConfig(ctx, region, accessKeyID, secretKey)
		if err != nil {
			return fmt.Errorf("seed %d: aws config: %w", i, err)
		}
		if err := applyS3Seed(ctx, newS3ClientFromConfig(cfg, endpointURL), region, doc.S3); err != nil {
			return fmt.Errorf("seed %d: s3: %w", i, err)
		}
		if err := applyIAMSeed(ctx, newIAMClient(cfg, endpointURL), doc.IAM); err != nil {
			return fmt.Errorf("seed %d: iam: %w", i, err)
		}
		state := newAWSSeedState()
		if err := applyEC2Seed(ctx, newEC2Client(cfg, endpointURL), doc.EC2, state); err != nil {
			return fmt.Errorf("seed %d: ec2: %w", i, err)
		}
		if err := applyELBV2Seed(ctx, newELBV2Client(cfg, endpointURL), doc.ELBV2, state); err != nil {
			return fmt.Errorf("seed %d: elbv2: %w", i, err)
		}
	}
	return nil
}

func applyS3Seed(ctx context.Context, client *s3.Client, region string, seed s3Seed) error {
	for _, bucket := range seed.Buckets {
		if err := createBucket(ctx, client, region, bucket.Name); err != nil {
			return fmt.Errorf("create bucket %q: %w", bucket.Name, err)
		}
		if bucket.PublicAccessBlock != nil {
			if err := putPublicAccessBlock(ctx, client, bucket.Name, *bucket.PublicAccessBlock); err != nil {
				return fmt.Errorf("put public access block %q: %w", bucket.Name, err)
			}
		}
		if bucket.Encryption {
			if err := putBucketEncryption(ctx, client, bucket.Name); err != nil {
				return fmt.Errorf("put encryption %q: %w", bucket.Name, err)
			}
		}
		if bucket.Policy != nil {
			policy, err := policyJSON(bucket.Policy)
			if err != nil {
				return fmt.Errorf("encode policy %q: %w", bucket.Name, err)
			}
			if _, err := client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
				Bucket: aws.String(bucket.Name),
				Policy: aws.String(policy),
			}); err != nil {
				return fmt.Errorf("put policy %q: %w", bucket.Name, err)
			}
		}
		for _, obj := range bucket.Objects {
			body, err := objectBody(obj)
			if err != nil {
				return fmt.Errorf("object %s/%s: %w", bucket.Name, obj.Key, err)
			}
			input := &s3.PutObjectInput{
				Bucket: aws.String(bucket.Name),
				Key:    aws.String(obj.Key),
				Body:   strings.NewReader(body),
			}
			if obj.ContentType != "" {
				input.ContentType = aws.String(obj.ContentType)
			}
			if _, err := client.PutObject(ctx, input); err != nil {
				return fmt.Errorf("put object %s/%s: %w", bucket.Name, obj.Key, err)
			}
		}
	}
	return nil
}

func createBucket(ctx context.Context, client *s3.Client, region, name string) error {
	input := &s3.CreateBucketInput{Bucket: aws.String(name)}
	if region != "" && region != defaultRegion {
		input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	_, err := client.CreateBucket(ctx, input)
	return err
}

func putPublicAccessBlock(ctx context.Context, client *s3.Client, bucket string, cfg publicAccessBlock) error {
	_, err := client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(cfg.BlockPublicACLs),
			IgnorePublicAcls:      aws.Bool(cfg.IgnorePublicACLs),
			BlockPublicPolicy:     aws.Bool(cfg.BlockPublicPolicy),
			RestrictPublicBuckets: aws.Bool(cfg.RestrictPublicBuckets),
		},
	})
	return err
}

func putBucketEncryption(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: aws.String(bucket),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{{
				ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
					SSEAlgorithm: s3types.ServerSideEncryptionAes256,
				},
			}},
		},
	})
	return err
}

func objectBody(obj s3Object) (string, error) {
	if obj.BodyBase64 == "" {
		return obj.Body, nil
	}
	raw, err := base64.StdEncoding.DecodeString(obj.BodyBase64)
	if err != nil {
		return "", fmt.Errorf("decode body_base64: %w", err)
	}
	return string(raw), nil
}

func policyJSON(policy any) (string, error) {
	if s, ok := policy.(string); ok {
		return s, nil
	}
	normalized, err := normalizeYAMLValue(policy)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func normalizeYAMLValue(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			n, err := normalizeYAMLValue(v)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("policy map key %v is %T, want string", k, k)
			}
			n, err := normalizeYAMLValue(v)
			if err != nil {
				return nil, err
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			n, err := normalizeYAMLValue(v)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		return v, nil
	}
}

func firstSeedRegion(seeds []seedDoc) string {
	for _, s := range seeds {
		if strings.TrimSpace(s.Region) != "" {
			return strings.TrimSpace(s.Region)
		}
	}
	return ""
}

func firstSeedAccountID(seeds []seedDoc) string {
	for _, s := range seeds {
		if strings.TrimSpace(s.AccountID) != "" {
			return strings.TrimSpace(s.AccountID)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
