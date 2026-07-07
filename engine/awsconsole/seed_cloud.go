package awsconsole

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type iamSeed struct {
	ManagedPolicies []iamManagedPolicy `yaml:"managed_policies,omitempty"`
	Users           []iamUser          `yaml:"users,omitempty"`
	Roles           []iamRole          `yaml:"roles,omitempty"`
}

type iamManagedPolicy struct {
	Name        string `yaml:"name"`
	Path        string `yaml:"path,omitempty"`
	Description string `yaml:"description,omitempty"`
	Document    any    `yaml:"document"`
}

type iamInlinePolicy struct {
	Name     string `yaml:"name"`
	Document any    `yaml:"document"`
}

type iamUser struct {
	Name             string            `yaml:"name"`
	Path             string            `yaml:"path,omitempty"`
	Tags             map[string]string `yaml:"tags,omitempty"`
	AttachedPolicies []string          `yaml:"attached_policies,omitempty"`
	InlinePolicies   []iamInlinePolicy `yaml:"inline_policies,omitempty"`
	AccessKeys       int32             `yaml:"access_keys,omitempty"`
}

type iamRole struct {
	Name                   string            `yaml:"name"`
	Path                   string            `yaml:"path,omitempty"`
	AssumeRolePolicy       any               `yaml:"assume_role_policy,omitempty"`
	Tags                   map[string]string `yaml:"tags,omitempty"`
	AttachedPolicies       []string          `yaml:"attached_policies,omitempty"`
	InlinePolicies         []iamInlinePolicy `yaml:"inline_policies,omitempty"`
	MaxSessionDurationSecs int32             `yaml:"max_session_duration_seconds,omitempty"`
}

type ec2Seed struct {
	VPCs []ec2VPC `yaml:"vpcs,omitempty"`
}

type ec2VPC struct {
	Name           string             `yaml:"name"`
	CIDRBlock      string             `yaml:"cidr_block"`
	Tags           map[string]string  `yaml:"tags,omitempty"`
	Subnets        []ec2Subnet        `yaml:"subnets,omitempty"`
	SecurityGroups []ec2SecurityGroup `yaml:"security_groups,omitempty"`
	NatGateways    []ec2NatGateway    `yaml:"nat_gateways,omitempty"`
}

type ec2Subnet struct {
	Name             string            `yaml:"name"`
	CIDRBlock        string            `yaml:"cidr_block"`
	AvailabilityZone string            `yaml:"availability_zone,omitempty"`
	Tags             map[string]string `yaml:"tags,omitempty"`
}

type ec2SecurityGroup struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Tags        map[string]string `yaml:"tags,omitempty"`
}

type ec2NatGateway struct {
	Name   string            `yaml:"name"`
	Subnet string            `yaml:"subnet"`
	Tags   map[string]string `yaml:"tags,omitempty"`
}

type elbv2Seed struct {
	LoadBalancers []elbv2LoadBalancer `yaml:"load_balancers,omitempty"`
}

type elbv2LoadBalancer struct {
	Name           string             `yaml:"name"`
	Type           string             `yaml:"type,omitempty"`   // application|network
	Scheme         string             `yaml:"scheme,omitempty"` // internet-facing|internal
	Subnets        []string           `yaml:"subnets"`
	SecurityGroups []string           `yaml:"security_groups,omitempty"`
	Tags           map[string]string  `yaml:"tags,omitempty"`
	TargetGroups   []elbv2TargetGroup `yaml:"target_groups,omitempty"`
}

type elbv2TargetGroup struct {
	Name             string            `yaml:"name"`
	VPC              string            `yaml:"vpc"`
	Protocol         string            `yaml:"protocol,omitempty"`
	Port             int32             `yaml:"port,omitempty"`
	TargetType       string            `yaml:"target_type,omitempty"` // instance|ip|lambda|alb
	HealthCheckPath  string            `yaml:"health_check_path,omitempty"`
	Tags             map[string]string `yaml:"tags,omitempty"`
	Targets          []elbv2Target     `yaml:"targets,omitempty"`
	ListenerPort     int32             `yaml:"listener_port,omitempty"`
	ListenerProtocol string            `yaml:"listener_protocol,omitempty"`
}

type elbv2Target struct {
	ID   string `yaml:"id"`
	Port int32  `yaml:"port,omitempty"`
}

type awsSeedState struct {
	vpcs           map[string]string
	subnets        map[string]string
	securityGroups map[string]string
	natGateways    map[string]string
}

func newAWSSeedState() *awsSeedState {
	return &awsSeedState{
		vpcs:           map[string]string{},
		subnets:        map[string]string{},
		securityGroups: map[string]string{},
		natGateways:    map[string]string{},
	}
}

func validateIAMSeed(file string, seed iamSeed) error {
	seenPolicies := map[string]bool{}
	for i, p := range seed.ManagedPolicies {
		if err := requireSeedName(file, fmt.Sprintf("iam.managed_policies[%d].name", i), p.Name); err != nil {
			return err
		}
		if p.Document == nil {
			return fmt.Errorf("seed %q: iam.managed_policies[%d].document is required", file, i)
		}
		if seenPolicies[p.Name] {
			return fmt.Errorf("seed %q: duplicate iam managed policy %q", file, p.Name)
		}
		seenPolicies[p.Name] = true
	}
	seenUsers := map[string]bool{}
	for i, u := range seed.Users {
		if err := requireSeedName(file, fmt.Sprintf("iam.users[%d].name", i), u.Name); err != nil {
			return err
		}
		if seenUsers[u.Name] {
			return fmt.Errorf("seed %q: duplicate iam user %q", file, u.Name)
		}
		seenUsers[u.Name] = true
		for j, p := range u.InlinePolicies {
			if err := validateInlinePolicy(file, fmt.Sprintf("iam.users[%d].inline_policies[%d]", i, j), p); err != nil {
				return err
			}
		}
	}
	seenRoles := map[string]bool{}
	for i, r := range seed.Roles {
		if err := requireSeedName(file, fmt.Sprintf("iam.roles[%d].name", i), r.Name); err != nil {
			return err
		}
		if seenRoles[r.Name] {
			return fmt.Errorf("seed %q: duplicate iam role %q", file, r.Name)
		}
		seenRoles[r.Name] = true
		for j, p := range r.InlinePolicies {
			if err := validateInlinePolicy(file, fmt.Sprintf("iam.roles[%d].inline_policies[%d]", i, j), p); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateInlinePolicy(file, path string, p iamInlinePolicy) error {
	if err := requireSeedName(file, path+".name", p.Name); err != nil {
		return err
	}
	if p.Document == nil {
		return fmt.Errorf("seed %q: %s.document is required", file, path)
	}
	return nil
}

func validateEC2Seed(file string, seed ec2Seed) error {
	seenVPCs := map[string]bool{}
	seenSubnets := map[string]bool{}
	seenSecurityGroups := map[string]bool{}
	seenNatGateways := map[string]bool{}
	for i, vpc := range seed.VPCs {
		if err := requireSeedName(file, fmt.Sprintf("ec2.vpcs[%d].name", i), vpc.Name); err != nil {
			return err
		}
		if strings.TrimSpace(vpc.CIDRBlock) == "" {
			return fmt.Errorf("seed %q: ec2.vpcs[%d].cidr_block is required", file, i)
		}
		if seenVPCs[vpc.Name] {
			return fmt.Errorf("seed %q: duplicate ec2 vpc %q", file, vpc.Name)
		}
		seenVPCs[vpc.Name] = true
		for j, subnet := range vpc.Subnets {
			if err := requireSeedName(file, fmt.Sprintf("ec2.vpcs[%d].subnets[%d].name", i, j), subnet.Name); err != nil {
				return err
			}
			if strings.TrimSpace(subnet.CIDRBlock) == "" {
				return fmt.Errorf("seed %q: ec2.vpcs[%d].subnets[%d].cidr_block is required", file, i, j)
			}
			if seenSubnets[subnet.Name] {
				return fmt.Errorf("seed %q: duplicate ec2 subnet %q", file, subnet.Name)
			}
			seenSubnets[subnet.Name] = true
		}
		for j, sg := range vpc.SecurityGroups {
			if err := requireSeedName(file, fmt.Sprintf("ec2.vpcs[%d].security_groups[%d].name", i, j), sg.Name); err != nil {
				return err
			}
			if seenSecurityGroups[sg.Name] {
				return fmt.Errorf("seed %q: duplicate ec2 security group %q", file, sg.Name)
			}
			seenSecurityGroups[sg.Name] = true
		}
		for j, nat := range vpc.NatGateways {
			if err := requireSeedName(file, fmt.Sprintf("ec2.vpcs[%d].nat_gateways[%d].name", i, j), nat.Name); err != nil {
				return err
			}
			if strings.TrimSpace(nat.Subnet) == "" {
				return fmt.Errorf("seed %q: ec2.vpcs[%d].nat_gateways[%d].subnet is required", file, i, j)
			}
			if seenNatGateways[nat.Name] {
				return fmt.Errorf("seed %q: duplicate ec2 nat gateway %q", file, nat.Name)
			}
			seenNatGateways[nat.Name] = true
		}
	}
	return nil
}

func validateELBV2Seed(file string, seed elbv2Seed) error {
	seenLBs := map[string]bool{}
	seenTGs := map[string]bool{}
	for i, lb := range seed.LoadBalancers {
		if err := requireSeedName(file, fmt.Sprintf("elbv2.load_balancers[%d].name", i), lb.Name); err != nil {
			return err
		}
		if len(lb.Subnets) == 0 {
			return fmt.Errorf("seed %q: elbv2.load_balancers[%d].subnets is required", file, i)
		}
		if seenLBs[lb.Name] {
			return fmt.Errorf("seed %q: duplicate elbv2 load balancer %q", file, lb.Name)
		}
		seenLBs[lb.Name] = true
		for j, tg := range lb.TargetGroups {
			if err := requireSeedName(file, fmt.Sprintf("elbv2.load_balancers[%d].target_groups[%d].name", i, j), tg.Name); err != nil {
				return err
			}
			if strings.TrimSpace(tg.VPC) == "" {
				return fmt.Errorf("seed %q: elbv2.load_balancers[%d].target_groups[%d].vpc is required", file, i, j)
			}
			if seenTGs[tg.Name] {
				return fmt.Errorf("seed %q: duplicate elbv2 target group %q", file, tg.Name)
			}
			seenTGs[tg.Name] = true
		}
	}
	return nil
}

func requireSeedName(file, path, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("seed %q: %s is required", file, path)
	}
	return nil
}

func applyIAMSeed(ctx context.Context, client *iam.Client, seed iamSeed) error {
	policyARNByName := map[string]string{}
	for _, p := range seed.ManagedPolicies {
		doc, err := policyJSON(p.Document)
		if err != nil {
			return fmt.Errorf("managed policy %q: %w", p.Name, err)
		}
		out, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
			PolicyName:     aws.String(p.Name),
			Path:           aws.String(defaultPath(p.Path)),
			Description:    aws.String(p.Description),
			PolicyDocument: aws.String(doc),
		})
		if err != nil {
			return fmt.Errorf("create managed policy %q: %w", p.Name, err)
		}
		if out.Policy == nil || out.Policy.Arn == nil {
			return fmt.Errorf("create managed policy %q: missing arn in response", p.Name)
		}
		policyARNByName[p.Name] = aws.ToString(out.Policy.Arn)
	}
	for _, u := range seed.Users {
		if _, err := client.CreateUser(ctx, &iam.CreateUserInput{
			UserName: aws.String(u.Name),
			Path:     aws.String(defaultPath(u.Path)),
			Tags:     iamTags(u.Tags),
		}); err != nil {
			return fmt.Errorf("create user %q: %w", u.Name, err)
		}
		for _, ref := range u.AttachedPolicies {
			arn, err := resolvePolicyARN(ref, policyARNByName)
			if err != nil {
				return fmt.Errorf("user %q attach policy %q: %w", u.Name, ref, err)
			}
			if _, err := client.AttachUserPolicy(ctx, &iam.AttachUserPolicyInput{
				UserName:  aws.String(u.Name),
				PolicyArn: aws.String(arn),
			}); err != nil {
				return fmt.Errorf("attach user policy %q to %q: %w", ref, u.Name, err)
			}
		}
		for _, p := range u.InlinePolicies {
			doc, err := policyJSON(p.Document)
			if err != nil {
				return fmt.Errorf("user %q inline policy %q: %w", u.Name, p.Name, err)
			}
			if _, err := client.PutUserPolicy(ctx, &iam.PutUserPolicyInput{
				UserName:       aws.String(u.Name),
				PolicyName:     aws.String(p.Name),
				PolicyDocument: aws.String(doc),
			}); err != nil {
				return fmt.Errorf("put user policy %q on %q: %w", p.Name, u.Name, err)
			}
		}
		for i := int32(0); i < u.AccessKeys; i++ {
			if _, err := client.CreateAccessKey(ctx, &iam.CreateAccessKeyInput{
				UserName: aws.String(u.Name),
			}); err != nil {
				return fmt.Errorf("create access key %d for user %q: %w", i+1, u.Name, err)
			}
		}
	}
	for _, r := range seed.Roles {
		assume, err := policyJSON(firstNonNil(r.AssumeRolePolicy, defaultAssumeRolePolicy()))
		if err != nil {
			return fmt.Errorf("role %q assume role policy: %w", r.Name, err)
		}
		input := &iam.CreateRoleInput{
			RoleName:                 aws.String(r.Name),
			Path:                     aws.String(defaultPath(r.Path)),
			AssumeRolePolicyDocument: aws.String(assume),
			Tags:                     iamTags(r.Tags),
		}
		if r.MaxSessionDurationSecs > 0 {
			input.MaxSessionDuration = aws.Int32(r.MaxSessionDurationSecs)
		}
		if _, err := client.CreateRole(ctx, input); err != nil {
			return fmt.Errorf("create role %q: %w", r.Name, err)
		}
		for _, ref := range r.AttachedPolicies {
			arn, err := resolvePolicyARN(ref, policyARNByName)
			if err != nil {
				return fmt.Errorf("role %q attach policy %q: %w", r.Name, ref, err)
			}
			if _, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
				RoleName:  aws.String(r.Name),
				PolicyArn: aws.String(arn),
			}); err != nil {
				return fmt.Errorf("attach role policy %q to %q: %w", ref, r.Name, err)
			}
		}
		for _, p := range r.InlinePolicies {
			doc, err := policyJSON(p.Document)
			if err != nil {
				return fmt.Errorf("role %q inline policy %q: %w", r.Name, p.Name, err)
			}
			if _, err := client.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
				RoleName:       aws.String(r.Name),
				PolicyName:     aws.String(p.Name),
				PolicyDocument: aws.String(doc),
			}); err != nil {
				return fmt.Errorf("put role policy %q on %q: %w", p.Name, r.Name, err)
			}
		}
	}
	return nil
}

func applyEC2Seed(ctx context.Context, client *ec2.Client, seed ec2Seed, state *awsSeedState) error {
	for _, vpc := range seed.VPCs {
		vpcOut, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
			CidrBlock: aws.String(vpc.CIDRBlock),
		})
		if err != nil {
			return fmt.Errorf("create vpc %q: %w", vpc.Name, err)
		}
		if vpcOut.Vpc == nil || vpcOut.Vpc.VpcId == nil {
			return fmt.Errorf("create vpc %q: missing vpc id in response", vpc.Name)
		}
		vpcID := aws.ToString(vpcOut.Vpc.VpcId)
		state.vpcs[vpc.Name] = vpcID
		if err := tagEC2Resource(ctx, client, vpcID, vpc.Name, vpc.Tags); err != nil {
			return fmt.Errorf("tag vpc %q: %w", vpc.Name, err)
		}
		for _, subnet := range vpc.Subnets {
			input := &ec2.CreateSubnetInput{
				VpcId:     aws.String(vpcID),
				CidrBlock: aws.String(subnet.CIDRBlock),
			}
			if subnet.AvailabilityZone != "" {
				input.AvailabilityZone = aws.String(subnet.AvailabilityZone)
			}
			subnetOut, err := client.CreateSubnet(ctx, input)
			if err != nil {
				return fmt.Errorf("create subnet %q: %w", subnet.Name, err)
			}
			if subnetOut.Subnet == nil || subnetOut.Subnet.SubnetId == nil {
				return fmt.Errorf("create subnet %q: missing subnet id in response", subnet.Name)
			}
			subnetID := aws.ToString(subnetOut.Subnet.SubnetId)
			state.subnets[subnet.Name] = subnetID
			if err := tagEC2Resource(ctx, client, subnetID, subnet.Name, subnet.Tags); err != nil {
				return fmt.Errorf("tag subnet %q: %w", subnet.Name, err)
			}
		}
		for _, sg := range vpc.SecurityGroups {
			sgOut, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
				GroupName:   aws.String(sg.Name),
				Description: aws.String(firstNonEmpty(sg.Description, "fab fixture security group")),
				VpcId:       aws.String(vpcID),
			})
			if err != nil {
				return fmt.Errorf("create security group %q: %w", sg.Name, err)
			}
			if sgOut.GroupId == nil {
				return fmt.Errorf("create security group %q: missing group id in response", sg.Name)
			}
			sgID := aws.ToString(sgOut.GroupId)
			state.securityGroups[sg.Name] = sgID
			if err := tagEC2Resource(ctx, client, sgID, sg.Name, sg.Tags); err != nil {
				return fmt.Errorf("tag security group %q: %w", sg.Name, err)
			}
		}
		for _, nat := range vpc.NatGateways {
			subnetID, ok := state.subnets[nat.Subnet]
			if !ok {
				return fmt.Errorf("nat gateway %q references unknown subnet %q", nat.Name, nat.Subnet)
			}
			allocOut, err := client.AllocateAddress(ctx, &ec2.AllocateAddressInput{Domain: ec2types.DomainTypeVpc})
			if err != nil {
				return fmt.Errorf("allocate eip for nat gateway %q: %w", nat.Name, err)
			}
			natOut, err := client.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
				AllocationId: allocOut.AllocationId,
				SubnetId:     aws.String(subnetID),
			})
			if err != nil {
				return fmt.Errorf("create nat gateway %q: %w", nat.Name, err)
			}
			if natOut.NatGateway == nil || natOut.NatGateway.NatGatewayId == nil {
				return fmt.Errorf("create nat gateway %q: missing nat gateway id in response", nat.Name)
			}
			natID := aws.ToString(natOut.NatGateway.NatGatewayId)
			state.natGateways[nat.Name] = natID
			if err := tagEC2Resource(ctx, client, natID, nat.Name, nat.Tags); err != nil {
				return fmt.Errorf("tag nat gateway %q: %w", nat.Name, err)
			}
		}
	}
	return nil
}

func applyELBV2Seed(ctx context.Context, client *elbv2.Client, seed elbv2Seed, state *awsSeedState) error {
	for _, lb := range seed.LoadBalancers {
		lbType := parseLoadBalancerType(lb.Type)
		subnets, err := resolveSeedIDs("subnet", lb.Subnets, state.subnets)
		if err != nil {
			return fmt.Errorf("load balancer %q: %w", lb.Name, err)
		}
		input := &elbv2.CreateLoadBalancerInput{
			Name:    aws.String(lb.Name),
			Scheme:  parseLoadBalancerScheme(lb.Scheme),
			Subnets: subnets,
			Tags:    elbv2Tags(lb.Name, lb.Tags),
			Type:    lbType,
		}
		if lbType == elbv2types.LoadBalancerTypeEnumApplication && len(lb.SecurityGroups) > 0 {
			securityGroups, err := resolveSeedIDs("security group", lb.SecurityGroups, state.securityGroups)
			if err != nil {
				return fmt.Errorf("load balancer %q: %w", lb.Name, err)
			}
			input.SecurityGroups = securityGroups
		}
		lbOut, err := client.CreateLoadBalancer(ctx, input)
		if err != nil {
			return fmt.Errorf("create load balancer %q: %w", lb.Name, err)
		}
		if len(lbOut.LoadBalancers) == 0 || lbOut.LoadBalancers[0].LoadBalancerArn == nil {
			return fmt.Errorf("create load balancer %q: missing arn in response", lb.Name)
		}
		lbARN := aws.ToString(lbOut.LoadBalancers[0].LoadBalancerArn)
		for _, tg := range lb.TargetGroups {
			vpcID, ok := state.vpcs[tg.VPC]
			if !ok {
				return fmt.Errorf("target group %q references unknown vpc %q", tg.Name, tg.VPC)
			}
			tgProtocol := parseELBV2Protocol(tg.Protocol, defaultTargetGroupProtocol(lbType))
			port := tg.Port
			if port == 0 {
				port = 80
			}
			tgInput := &elbv2.CreateTargetGroupInput{
				Name:       aws.String(tg.Name),
				Port:       aws.Int32(port),
				Protocol:   tgProtocol,
				TargetType: parseTargetType(tg.TargetType),
				Tags:       elbv2Tags(tg.Name, tg.Tags),
				VpcId:      aws.String(vpcID),
			}
			if tg.HealthCheckPath != "" && (tgProtocol == elbv2types.ProtocolEnumHttp || tgProtocol == elbv2types.ProtocolEnumHttps) {
				tgInput.HealthCheckPath = aws.String(tg.HealthCheckPath)
			}
			tgOut, err := client.CreateTargetGroup(ctx, tgInput)
			if err != nil {
				return fmt.Errorf("create target group %q: %w", tg.Name, err)
			}
			if len(tgOut.TargetGroups) == 0 || tgOut.TargetGroups[0].TargetGroupArn == nil {
				return fmt.Errorf("create target group %q: missing arn in response", tg.Name)
			}
			tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)
			if len(tg.Targets) > 0 {
				targets := make([]elbv2types.TargetDescription, 0, len(tg.Targets))
				for _, target := range tg.Targets {
					targetPort := target.Port
					if targetPort == 0 {
						targetPort = port
					}
					targets = append(targets, elbv2types.TargetDescription{
						Id:   aws.String(target.ID),
						Port: aws.Int32(targetPort),
					})
				}
				if _, err := client.RegisterTargets(ctx, &elbv2.RegisterTargetsInput{
					TargetGroupArn: aws.String(tgARN),
					Targets:        targets,
				}); err != nil {
					return fmt.Errorf("register targets for %q: %w", tg.Name, err)
				}
			}
			if tg.ListenerPort > 0 {
				if _, err := client.CreateListener(ctx, &elbv2.CreateListenerInput{
					LoadBalancerArn: aws.String(lbARN),
					Port:            aws.Int32(tg.ListenerPort),
					Protocol:        parseELBV2Protocol(tg.ListenerProtocol, tgProtocol),
					DefaultActions: []elbv2types.Action{{
						Type:           elbv2types.ActionTypeEnumForward,
						TargetGroupArn: aws.String(tgARN),
					}},
				}); err != nil {
					return fmt.Errorf("create listener for %q on %q: %w", tg.Name, lb.Name, err)
				}
			}
		}
	}
	return nil
}

func resolveSeedIDs(kind string, names []string, lookup map[string]string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if id, ok := lookup[name]; ok {
			out = append(out, id)
		} else {
			return nil, fmt.Errorf("%s %q is not defined in this seed", kind, name)
		}
	}
	return out, nil
}

func resolvePolicyARN(ref string, policies map[string]string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty policy reference")
	}
	if strings.HasPrefix(ref, "arn:") {
		return ref, nil
	}
	if arn, ok := policies[ref]; ok {
		return arn, nil
	}
	return "", fmt.Errorf("unknown managed policy %q", ref)
}

func tagEC2Resource(ctx context.Context, client *ec2.Client, resourceID, name string, tags map[string]string) error {
	tagList := ec2Tags(name, tags)
	if len(tagList) == 0 {
		return nil
	}
	_, err := client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{resourceID},
		Tags:      tagList,
	})
	return err
}

func iamTags(tags map[string]string) []iamtypes.Tag {
	keys := sortedTagKeys(tags, "")
	out := make([]iamtypes.Tag, 0, len(keys))
	for _, k := range keys {
		out = append(out, iamtypes.Tag{Key: aws.String(k), Value: aws.String(tags[k])})
	}
	return out
}

func ec2Tags(name string, tags map[string]string) []ec2types.Tag {
	keys := sortedTagKeys(tags, name)
	out := make([]ec2types.Tag, 0, len(keys))
	for _, k := range keys {
		out = append(out, ec2types.Tag{Key: aws.String(k), Value: aws.String(tagValue(k, name, tags))})
	}
	return out
}

func elbv2Tags(name string, tags map[string]string) []elbv2types.Tag {
	keys := sortedTagKeys(tags, name)
	out := make([]elbv2types.Tag, 0, len(keys))
	for _, k := range keys {
		out = append(out, elbv2types.Tag{Key: aws.String(k), Value: aws.String(tagValue(k, name, tags))})
	}
	return out
}

func sortedTagKeys(tags map[string]string, name string) []string {
	seen := map[string]bool{}
	if name != "" {
		seen["Name"] = true
	}
	for k := range tags {
		if strings.TrimSpace(k) != "" {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func tagValue(key, name string, tags map[string]string) string {
	if v, ok := tags[key]; ok {
		return v
	}
	if key == "Name" {
		return name
	}
	return ""
}

func defaultPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	return path
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func defaultAssumeRolePolicy() any {
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{
				"Effect": "Allow",
				"Principal": map[string]any{
					"Service": "ec2.amazonaws.com",
				},
				"Action": "sts:AssumeRole",
			},
		},
	}
}

func parseLoadBalancerType(value string) elbv2types.LoadBalancerTypeEnum {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "network", "nlb":
		return elbv2types.LoadBalancerTypeEnumNetwork
	default:
		return elbv2types.LoadBalancerTypeEnumApplication
	}
}

func parseLoadBalancerScheme(value string) elbv2types.LoadBalancerSchemeEnum {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "internal":
		return elbv2types.LoadBalancerSchemeEnumInternal
	default:
		return elbv2types.LoadBalancerSchemeEnumInternetFacing
	}
}

func parseTargetType(value string) elbv2types.TargetTypeEnum {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "instance":
		return elbv2types.TargetTypeEnumInstance
	case "lambda":
		return elbv2types.TargetTypeEnumLambda
	case "alb":
		return elbv2types.TargetTypeEnumAlb
	default:
		return elbv2types.TargetTypeEnumIp
	}
}

func defaultTargetGroupProtocol(lbType elbv2types.LoadBalancerTypeEnum) elbv2types.ProtocolEnum {
	if lbType == elbv2types.LoadBalancerTypeEnumNetwork {
		return elbv2types.ProtocolEnumTcp
	}
	return elbv2types.ProtocolEnumHttp
}

func parseELBV2Protocol(value string, fallback elbv2types.ProtocolEnum) elbv2types.ProtocolEnum {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "HTTP":
		return elbv2types.ProtocolEnumHttp
	case "HTTPS":
		return elbv2types.ProtocolEnumHttps
	case "TCP":
		return elbv2types.ProtocolEnumTcp
	case "TLS":
		return elbv2types.ProtocolEnumTls
	case "UDP":
		return elbv2types.ProtocolEnumUdp
	case "TCP_UDP":
		return elbv2types.ProtocolEnumTcpUdp
	case "GENEVE":
		return elbv2types.ProtocolEnumGeneve
	default:
		return fallback
	}
}
