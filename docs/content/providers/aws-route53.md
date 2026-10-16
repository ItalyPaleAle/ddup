---
title: "AWS Route 53"
weight: 10
---

ddup can manage records in a hosted zone on [Amazon Route 53](https://aws.amazon.com/route53/). It needs the ID of the hosted zone, and AWS credentials that are allowed to list and change the zone's records.

## Example

This configuration uses the credentials that the AWS SDK finds in the environment, such as an IAM role when ddup runs on AWS:

```yaml
providers:
  route53-main:
    awsRoute53:
      hostedZoneId: "Z0123456789ABCDEFGHIJ"

domains:
  - recordName: "app.example.com"
    provider: "route53-main"
    endpoints:
      - name: "web1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "web2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
```

See [Authentication](#authentication) to use an access key instead.

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `hostedZoneId` | string | **required** | ID of the hosted zone, such as `Z0123456789ABCDEFGHIJ`. The `/hostedzone/Z0123456789ABCDEFGHIJ` format returned by the AWS CLI works too. |
| `accessKeyId` | string | | Access key ID of an IAM user. Set it together with `secretAccessKey`. |
| `secretAccessKey` | string | | Secret access key of the IAM user |

Each provider entry manages one hosted zone. To manage records in several zones, add an entry for each zone.

## Getting the hosted zone ID

In the [Route 53 console](https://console.aws.amazon.com/route53/v2/hostedzones), the ID is in the **Hosted zone ID** column of the list of hosted zones.

With the AWS CLI:

```sh
aws route53 list-hosted-zones-by-name \
  --dns-name example.com \
  --query "HostedZones[0].Id" --output text
```

Both public and private hosted zones work. If you have a public and a private zone with the same name, make sure you pick the right one.

## Authentication

ddup picks how to authenticate based on the options you set:

| Options set | Authentication |
|-------------|----------------|
| `accessKeyId` and `secretAccessKey` | [Access key](#access-key) of an IAM user |
| Neither | [Default credential chain](#default-credential-chain) of the AWS SDK |

Whichever method you choose, the identity needs the permissions in [Permissions](#permissions).

At startup, ddup logs which method it's using: `Authenticating to AWS with static credentials` or `Authenticating to AWS with the default credential chain`.

### Access key

Use an access key when ddup runs outside of AWS. Save the [policy](#permissions) as `ddup-policy.json`, then create an IAM user with that policy and an access key:

```sh
aws iam create-user --user-name ddup

aws iam put-user-policy \
  --user-name ddup \
  --policy-name ddup-route53 \
  --policy-document file://ddup-policy.json

aws iam create-access-key --user-name ddup
```

The output of the last command contains the `AccessKeyId` and the `SecretAccessKey`:

```yaml
providers:
  route53-main:
    awsRoute53:
      hostedZoneId: "Z0123456789ABCDEFGHIJ"
      accessKeyId: "AKIAIOSFODNN7EXAMPLE"
      secretAccessKey: !file /run/secrets/aws-secret-access-key
```

Access keys don't expire on their own, so anyone who gets the key can use it until you delete it. When ddup runs on AWS, use a role instead.

### Default credential chain

When you don't set `accessKeyId` and `secretAccessKey`, ddup looks for credentials the same way as the AWS CLI and SDKs, in places that include:

- The `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` environment variables, plus `AWS_SESSION_TOKEN` for temporary credentials
- The shared configuration and credentials files in `~/.aws`, with the profile in `AWS_PROFILE`
- IAM roles for service accounts on Amazon EKS
- Task roles on Amazon ECS
- Instance profiles on Amazon EC2

See the [AWS documentation](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html) for the full list.

For example, to pass an access key as environment variables with Docker:

```sh
docker run -d \
  --name ddup \
  --read-only \
  -v $HOME/.ddup:/etc/ddup:ro \
  -e AWS_ACCESS_KEY_ID="AKIAIOSFODNN7EXAMPLE" \
  -e AWS_SECRET_ACCESS_KEY="your-secret-access-key" \
  ghcr.io/italypaleale/ddup:v0
```

## Permissions

The identity that ddup uses needs permission to list and change the record sets of the hosted zone. Replace `<your-zone-id>` with the ID of your hosted zone:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "route53:ListResourceRecordSets",
        "route53:ChangeResourceRecordSets"
      ],
      "Resource": "arn:aws:route53:::hostedzone/<your-zone-id>"
    }
  ]
}
```

This policy allows changing every record in the zone. To allow only the names and record types that ddup manages, add conditions on `ChangeResourceRecordSets`. List the names in lowercase, without the trailing dot:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "route53:ListResourceRecordSets",
      "Resource": "arn:aws:route53:::hostedzone/Z0123456789ABCDEFGHIJ"
    },
    {
      "Effect": "Allow",
      "Action": "route53:ChangeResourceRecordSets",
      "Resource": "arn:aws:route53:::hostedzone/Z0123456789ABCDEFGHIJ",
      "Condition": {
        "ForAllValues:StringEquals": {
          "route53:ChangeResourceRecordSetsNormalizedRecordNames": [
            "app.example.com"
          ],
          "route53:ChangeResourceRecordSetsRecordTypes": [
            "A",
            "AAAA"
          ]
        }
      }
    }
  ]
}
```

## Things to know

- Only record sets with simple routing are supported. If the name already has an alias record, or a record set with another routing policy such as weighted, latency, or failover, the update fails with an error.
- The A and AAAA record sets with the domain's name are replaced, including records you created in the console. See [How ddup manages records](/providers#how-ddup-manages-records).
