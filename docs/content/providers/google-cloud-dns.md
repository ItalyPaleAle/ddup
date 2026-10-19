---
title: "Google Cloud DNS"
weight: 40
---

ddup can manage records in a managed zone on [Google Cloud DNS](https://cloud.google.com/dns). It needs the ID of the project, the name of the managed zone, and a principal that is allowed to change the zone's records.

## Example

This configuration uses the credentials that ddup finds in the environment, such as the service account attached to the virtual machine when ddup runs on Google Cloud:

```yaml
providers:
  gcp-main:
    googleCloudDns:
      projectId: "my-project"
      managedZone: "example-com"

domains:
  - recordName: "app.example.com"
    provider: "gcp-main"
    endpoints:
      - name: "web1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "web2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
```

See [Authentication](#authentication) to use a service account key instead.

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `projectId` | string | **required** | ID of the Google Cloud project that contains the managed zone |
| `managedZone` | string | **required** | Name of the managed zone, such as `example-com`. This is the name of the resource in Google Cloud, not the domain name. |
| `credentialsFile` | string | | Path to the JSON key file of a service account |

Each provider entry manages one zone. To manage records in several zones, add an entry for each zone.

## Getting the managed zone name

Each managed zone has a name, which you chose when you created it, and a DNS name, which is the domain. ddup needs the name. To see both:

```sh
gcloud dns managed-zones list --project my-project
```

```text
NAME         DNS_NAME      DESCRIPTION  VISIBILITY
example-com  example.com.               public
```

You can also find it in the [Cloud DNS console](https://console.cloud.google.com/net-services/dns/zones), in the **Zone name** column.

Both public and private zones work.

## Authentication

| Options set | Authentication |
|-------------|----------------|
| `credentialsFile` | [Service account key](#service-account-key) |
| None | [Application Default Credentials](#application-default-credentials) |

Whichever method you choose, the principal needs the permissions in [Permissions](#permissions).

At startup, ddup logs which method it's using: `Authenticating to Google Cloud with a service account key` or `Authenticating to Google Cloud with Application Default Credentials`.

### Service account key

Use a service account key when ddup runs outside of Google Cloud. Create a service account, allow it to change the zone, and create a key for it:

```sh
gcloud iam service-accounts create ddup --project my-project

gcloud dns managed-zones add-iam-policy-binding example-com \
  --project my-project \
  --member "serviceAccount:ddup@my-project.iam.gserviceaccount.com" \
  --role "roles/dns.admin"

gcloud iam service-accounts keys create ddup-key.json \
  --iam-account ddup@my-project.iam.gserviceaccount.com
```

Then set `credentialsFile` to the path of the key file. When ddup runs in a container, you can put the key in the same folder as `config.yaml`, which is mounted at `/etc/ddup`:

```yaml
providers:
  gcp-main:
    googleCloudDns:
      projectId: "my-project"
      managedZone: "example-com"
      credentialsFile: "/etc/ddup/ddup-key.json"
```

- Use an absolute path. Unlike [`!file`](/docs/configuration#secrets), a relative path in `credentialsFile` is relative to the current folder, not to the folder of the configuration file.
- `credentialsFile` accepts service account keys only. For other types of credential files, such as a Workload Identity Federation configuration, set the `GOOGLE_APPLICATION_CREDENTIALS` environment variable instead (see below).
- Keys don't expire unless your organization sets a policy for it, and some organizations don't allow creating keys at all. When ddup runs on Google Cloud, prefer Application Default Credentials.

### Application Default Credentials

When you don't set `credentialsFile`, ddup uses [Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials), which look for credentials in places that include:

- The `GOOGLE_APPLICATION_CREDENTIALS` environment variable, with the path to a service account key or to a Workload Identity Federation configuration file
- The service account attached to the resource that runs ddup, such as a Compute Engine virtual machine or a Cloud Run service, or the one bound to a Kubernetes service account with Workload Identity Federation for GKE
- Your user account, after you run `gcloud auth application-default login`, which is handy for trying ddup on your computer

On Compute Engine, the virtual machine's access scopes must allow Cloud DNS. The default access scopes don't: set the access scopes to allow full access to all Cloud APIs, or include the Cloud DNS scope.

## Permissions

The principal that ddup uses needs the **DNS Administrator** role (`roles/dns.admin`). Grant it on the managed zone rather than on the project, so ddup can change only that zone:

```sh
gcloud dns managed-zones add-iam-policy-binding example-com \
  --project my-project \
  --member "serviceAccount:ddup@my-project.iam.gserviceaccount.com" \
  --role "roles/dns.admin"
```

To allow only what ddup needs, create a [custom role](https://cloud.google.com/iam/docs/creating-custom-roles) with these permissions:

```text
dns.changes.create
dns.resourceRecordSets.list
dns.resourceRecordSets.create
dns.resourceRecordSets.update
dns.resourceRecordSets.delete
```

## Things to know

- Record sets with a routing policy aren't supported. If the name already has one, such as weighted round robin or geolocation, the update fails with an error.
- The A and AAAA record sets with the domain's name are replaced, including records you created in the console. See [How ddup manages records](/providers#how-ddup-manages-records).
