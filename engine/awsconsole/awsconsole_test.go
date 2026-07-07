package awsconsole

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dumbmachine/fabricate/profile"
)

func TestLoadSeedDocsParsesMotoSeed(t *testing.T) {
	p := &profile.Profile{
		Name:   "aws-fixture",
		Engine: Engine,
		FS: fstest.MapFS{
			"seed.yaml": &fstest.MapFile{Data: []byte(`
account_id: "123456789012"
region: us-east-1
s3:
  buckets:
    - name: public-assets
      public_access_block:
        block_public_acls: false
        ignore_public_acls: false
        block_public_policy: false
        restrict_public_buckets: false
      encryption: true
      policy:
        Version: "2012-10-17"
        Statement:
          - Effect: Allow
            Principal: "*"
            Action: s3:GetObject
            Resource: arn:aws:s3:::public-assets/*
      objects:
        - key: readme.txt
          content_type: text/plain
          body: hello
iam:
  managed_policies:
    - name: LegacyBroadOps
      document:
        Version: "2012-10-17"
        Statement:
          - Effect: Allow
            Action: "*"
            Resource: "*"
  users:
    - name: contractor-2024-mary
      tags:
        Owner: security@example.com
        ExpectedFinding: unused-broad-access
      attached_policies: [LegacyBroadOps]
      access_keys: 1
  roles:
    - name: legacy-deploy
      attached_policies: [LegacyBroadOps]
ec2:
  vpcs:
    - name: dev-vpc
      cidr_block: 10.20.0.0/16
      subnets:
        - name: dev-public-a
          cidr_block: 10.20.1.0/24
          availability_zone: us-east-1a
      security_groups:
        - name: legacy-lb-sg
      nat_gateways:
        - name: nat-dev-idle
          subnet: dev-public-a
          tags:
            ExpectedFinding: idle-nat
elbv2:
  load_balancers:
    - name: dashboard-legacy-alb
      type: application
      subnets: [dev-public-a]
      security_groups: [legacy-lb-sg]
      target_groups:
        - name: dashboard-legacy-empty
          vpc: dev-vpc
          protocol: HTTP
          port: 80
          listener_port: 80
`)},
		},
		Seed: []profile.SeedStep{{Type: profile.SeedTypeMotoSeed, File: "seed.yaml"}},
	}

	docs, err := loadSeedDocs(p)
	if err != nil {
		t.Fatalf("loadSeedDocs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("docs len = %d, want 1", len(docs))
	}
	buckets := docs[0].S3.Buckets
	if len(buckets) != 1 || buckets[0].Name != "public-assets" || !buckets[0].Encryption {
		t.Fatalf("unexpected buckets: %+v", buckets)
	}
	if buckets[0].PublicAccessBlock == nil {
		t.Fatal("public access block not parsed")
	}
	policy, err := policyJSON(buckets[0].Policy)
	if err != nil {
		t.Fatalf("policyJSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(policy), &decoded); err != nil {
		t.Fatalf("policy is not valid JSON: %v: %s", err, policy)
	}
	if decoded["Version"] != "2012-10-17" {
		t.Fatalf("policy version = %v", decoded["Version"])
	}
	if got := len(docs[0].IAM.Users); got != 1 {
		t.Fatalf("iam users = %d, want 1", got)
	}
	if got := len(docs[0].EC2.VPCs); got != 1 {
		t.Fatalf("ec2 vpcs = %d, want 1", got)
	}
	if got := len(docs[0].ELBV2.LoadBalancers); got != 1 {
		t.Fatalf("elbv2 load balancers = %d, want 1", got)
	}
}

func TestLoadSeedDocsRejectsUnsupportedSeedType(t *testing.T) {
	p := &profile.Profile{
		Name:   "aws-fixture",
		Engine: Engine,
		Seed:   []profile.SeedStep{{Type: profile.SeedTypeSQL, File: "seed.sql"}},
	}
	_, err := loadSeedDocs(p)
	if err == nil || !strings.Contains(err.Error(), profile.SeedTypeMotoSeed) {
		t.Fatalf("expected moto-seed error, got %v", err)
	}
}

func TestSeedDocValidateRejectsDuplicateObjects(t *testing.T) {
	doc := seedDoc{S3: s3Seed{Buckets: []s3Bucket{{
		Name: "bucket-a",
		Objects: []s3Object{
			{Key: "same.txt", Body: "a"},
			{Key: "same.txt", Body: "b"},
		},
	}}}}
	err := doc.validate("seed.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate object") {
		t.Fatalf("expected duplicate object error, got %v", err)
	}
}

func TestSeedDocValidateRejectsDuplicateCloudFixtures(t *testing.T) {
	doc := seedDoc{
		IAM: iamSeed{Users: []iamUser{{Name: "same"}, {Name: "same"}}},
	}
	err := doc.validate("seed.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate iam user") {
		t.Fatalf("expected duplicate iam user error, got %v", err)
	}

	doc = seedDoc{
		EC2: ec2Seed{VPCs: []ec2VPC{{Name: "vpc-a", CIDRBlock: "10.0.0.0/16"}, {Name: "vpc-a", CIDRBlock: "10.1.0.0/16"}}},
	}
	err = doc.validate("seed.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate ec2 vpc") {
		t.Fatalf("expected duplicate ec2 vpc error, got %v", err)
	}

	doc = seedDoc{
		ELBV2: elbv2Seed{LoadBalancers: []elbv2LoadBalancer{{Name: "same", Subnets: []string{"subnet-a"}}, {Name: "same", Subnets: []string{"subnet-b"}}}},
	}
	err = doc.validate("seed.yaml")
	if err == nil || !strings.Contains(err.Error(), "duplicate elbv2 load balancer") {
		t.Fatalf("expected duplicate elbv2 load balancer error, got %v", err)
	}
}

func TestObjectBodyBase64(t *testing.T) {
	got, err := objectBody(s3Object{Key: "x", BodyBase64: "aGVsbG8="})
	if err != nil {
		t.Fatalf("objectBody: %v", err)
	}
	if got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}
