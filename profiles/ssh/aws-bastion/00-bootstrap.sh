#!/bin/sh
# Bootstrap an AWS-bastion-looking sandbox. POSIX-sh only — the
# linuxserver/openssh-server base is Alpine without bash.
set -eu

mkdir -p /root/.aws /etc/profile.d /var/log /usr/local/bin

# Fake AWS CLI. Dispatches on the first two args and prints canned
# JSON. Enough surface to rehearse "what do I run when…" without
# pretending to be the real CLI.
cat > /usr/local/bin/aws <<'AWS'
#!/bin/sh
case "$1 $2" in
  "ec2 describe-instances")
    cat <<'JSON'
{
  "Reservations": [
    { "Instances": [
      { "InstanceId": "i-0a1b2c3d4e5f60001", "State": {"Name":"running"},
        "InstanceType": "m6i.large", "Tags": [{"Key":"role","Value":"api"}],
        "PrivateIpAddress": "10.0.1.14", "LaunchTime": "2024-06-01T09:00:00Z" } ] },
    { "Instances": [
      { "InstanceId": "i-0a1b2c3d4e5f60002", "State": {"Name":"running"},
        "InstanceType": "m6i.large", "Tags": [{"Key":"role","Value":"api"}],
        "PrivateIpAddress": "10.0.1.15", "LaunchTime": "2024-06-01T09:00:00Z" } ] },
    { "Instances": [
      { "InstanceId": "i-0a1b2c3d4e5f60099", "State": {"Name":"stopped"},
        "InstanceType": "t3.small",  "Tags": [{"Key":"role","Value":"bastion"}],
        "PrivateIpAddress": "10.0.0.4",  "LaunchTime": "2023-12-12T11:00:00Z" } ] }
  ]
}
JSON
    ;;
  "s3 ls")
    echo "2024-05-12 10:11:12  reports-prod"
    echo "2024-05-12 10:11:12  reports-staging"
    echo "2024-05-12 10:11:12  audit-logs"
    echo "2024-05-12 10:11:12  backups-pg"
    ;;
  "logs tail")
    # third arg is the log group name; the fourth-and-beyond are flags
    log="${3:-/aws/lambda/checkout}"
    printf '2024-06-01T09:00:01.000Z %s START RequestId: 1a2b3c4d Version: $LATEST\n' "$log"
    printf '2024-06-01T09:00:01.043Z %s INFO  request received { "amount_cents": 1899 }\n' "$log"
    printf '2024-06-01T09:00:01.082Z %s INFO  charged customer cus_001\n' "$log"
    printf '2024-06-01T09:00:01.090Z %s ERROR downstream payment processor timed out\n' "$log"
    printf '2024-06-01T09:00:01.100Z %s INFO  retry scheduled (attempt 2/3)\n' "$log"
    ;;
  "sts get-caller-identity")
    echo '{"UserId":"AIDAEXAMPLEFAKEID","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/fab-bastion"}'
    ;;
  *)
    echo "fab-fake-aws: command not seeded: $* (run 'aws help' on a real CLI)" >&2
    exit 2
    ;;
esac
AWS
chmod 0755 /usr/local/bin/aws

# Fake credentials — clearly marked so nobody confuses them with
# real ones. Shape only.
cat > /root/.aws/credentials <<'CREDS'
[default]
# These are not real credentials. fab generates this file so the
# bastion has the right shape for AWS_PROFILE-style tooling.
aws_access_key_id     = AKIAEXAMPLEFAKE000000
aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
CREDS
chmod 0600 /root/.aws/credentials

cat > /root/.aws/config <<'CFG'
[default]
region = us-east-1
output = json
CFG
chmod 0644 /root/.aws/config

cat > /etc/profile.d/aws.sh <<'PROFILE'
export AWS_REGION=us-east-1
export AWS_DEFAULT_REGION=us-east-1
export AWS_PROFILE=default
PROFILE
chmod 0644 /etc/profile.d/aws.sh

cat > /etc/motd <<'MOTD'
You are on bastion-01 (us-east-1, vpc-04ce…).
A canned `aws` CLI lives at /usr/local/bin/aws — describe-instances,
s3 ls, logs tail, sts get-caller-identity. Everything else is
intentionally not seeded.
MOTD
chmod 0644 /etc/motd

# A few synthetic auth-log lines, the kind a security agent would
# grep for unusual sources.
cat > /var/log/secure <<'LOG'
Jun  1 08:59:01 bastion-01 sshd[1023]: Accepted publickey for fab from 10.0.0.4 port 51234 ssh2: ED25519 SHA256:abcd…
Jun  1 09:14:55 bastion-01 sshd[1099]: Accepted publickey for fab from 10.0.0.4 port 51235 ssh2: ED25519 SHA256:abcd…
Jun  1 11:02:11 bastion-01 sshd[1187]: Failed password for invalid user admin from 203.0.113.10 port 44233 ssh2
Jun  1 11:02:14 bastion-01 sshd[1187]: Failed password for invalid user admin from 203.0.113.10 port 44233 ssh2
Jun  1 11:02:17 bastion-01 sshd[1187]: Failed password for invalid user admin from 203.0.113.10 port 44233 ssh2
Jun  1 11:02:20 bastion-01 sshd[1187]: Disconnecting authenticating user: 3 failures from 203.0.113.10
Jun  1 14:31:42 bastion-01 sshd[1402]: Accepted publickey for fab from 10.0.0.4 port 51290 ssh2: ED25519 SHA256:abcd…
LOG
chmod 0644 /var/log/secure
