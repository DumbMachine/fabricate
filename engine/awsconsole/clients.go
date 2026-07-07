package awsconsole

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func newAWSConfig(ctx context.Context, region, accessKeyID, secretKey string) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, "")),
	)
}

func newS3Client(ctx context.Context, endpointURL, region, accessKeyID, secretKey string) (*s3.Client, error) {
	cfg, err := newAWSConfig(ctx, region, accessKeyID, secretKey)
	if err != nil {
		return nil, err
	}
	return newS3ClientFromConfig(cfg, endpointURL), nil
}

func newS3ClientFromConfig(cfg aws.Config, endpointURL string) *s3.Client {
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
		o.UsePathStyle = true
	})
}

func newIAMClient(cfg aws.Config, endpointURL string) *iam.Client {
	return iam.NewFromConfig(cfg, func(o *iam.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
	})
}

func newEC2Client(cfg aws.Config, endpointURL string) *ec2.Client {
	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
	})
}

func newELBV2Client(cfg aws.Config, endpointURL string) *elbv2.Client {
	return elbv2.NewFromConfig(cfg, func(o *elbv2.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
	})
}
