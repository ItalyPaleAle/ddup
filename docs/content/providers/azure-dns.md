---
title: "Azure DNS"
weight: 20
---

ddup can manage records in a public DNS zone hosted on [Azure DNS](https://learn.microsoft.com/azure/dns/dns-overview). It needs an identity in Microsoft Entra ID that has permission to change the zone's records: a service principal, a managed identity, or any identity supported by the Azure SDK.

Private DNS zones aren't supported.

## Example

This configuration uses a service principal:

```yaml
providers:
  azure-main:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "my-dns-rg"
      zoneName: "example.com"
      tenantId: "00000000-0000-0000-0000-000000000000"
      clientId: "00000000-0000-0000-0000-000000000000"
      clientSecret: !file /run/secrets/azure-client-secret

domains:
  - recordName: "app.example.com"
    provider: "azure-main"
    endpoints:
      - name: "web1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "web2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
```

See [Authentication](#authentication) for the other ways to authenticate.

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `subscriptionId` | string | **required** | ID of the Azure subscription that contains the DNS zone |
| `resourceGroupName` | string | **required** | Name of the resource group that contains the DNS zone |
| `zoneName` | string | **required** | Name of the DNS zone, which is the domain name, such as `example.com` |
| `tenantId` | string | | ID of the Microsoft Entra tenant. Required with `clientId` and `clientSecret`. |
| `clientId` | string | | Client ID (application ID) of a service principal |
| `clientSecret` | string | | Client secret of the service principal |
| `managedIdentityClientId` | string | | Client ID of a user-assigned managed identity |

Each provider entry manages one zone. To manage records in several zones, add an entry for each zone.

## Authentication

ddup picks how to authenticate based on the options you set:

| Options set | Authentication |
|-------------|----------------|
| `clientId` and `clientSecret` | [Service principal](#service-principal), with the tenant in `tenantId` |
| `managedIdentityClientId` | [User-assigned managed identity](#managed-identity) |
| None of the above | [Default credentials](#default-credentials) |

Whichever you choose, the identity needs permission to change the zone's records: see [Permissions](#permissions).

### Service principal

Use a service principal when ddup runs outside of Azure.

The quickest way to create one, with access to the zone only, is with the Azure CLI:

```sh
# Get the resource ID of the zone
ZONE_ID=$(az network dns zone show \
  --resource-group my-dns-rg \
  --name example.com \
  --query id --output tsv)

# Create the service principal, and allow it to change the zone's records
az ad sp create-for-rbac \
  --name ddup \
  --role "DNS Zone Contributor" \
  --scopes "$ZONE_ID"
```

The output contains the values for the configuration: `appId` is the `clientId`, `password` is the `clientSecret`, and `tenant` is the `tenantId`.

```yaml
providers:
  azure-main:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "my-dns-rg"
      zoneName: "example.com"
      tenantId: "00000000-0000-0000-0000-000000000000"
      clientId: "00000000-0000-0000-0000-000000000000"
      clientSecret: !file /run/secrets/azure-client-secret
```

Client secrets expire: when the secret expires, ddup can't update DNS anymore. Remember to create a new one before then.

### Managed identity

When ddup runs on Azure, for example on a virtual machine or in Azure Container Apps, it can authenticate with a [managed identity](https://learn.microsoft.com/entra/identity/managed-identities-azure-resources/overview), so there are no secrets to manage.

For a user-assigned managed identity, set its client ID:

```yaml
providers:
  azure-main:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "my-dns-rg"
      zoneName: "example.com"
      managedIdentityClientId: "00000000-0000-0000-0000-000000000000"
```

For a system-assigned managed identity, don't set any authentication option: ddup finds it with the [default credentials](#default-credentials).

### Default credentials

When you don't set `clientId`, `clientSecret`, or `managedIdentityClientId`, ddup tries the authentication methods of the Azure SDK in order, and uses the first one that works. They include:

- The `AZURE_TENANT_ID` and `AZURE_CLIENT_ID` environment variables, with either `AZURE_CLIENT_SECRET`, or `AZURE_CLIENT_CERTIFICATE_PATH` for a certificate (see the [full list](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity#readme-environment-variables))
- Workload identity, for example on Azure Kubernetes Service
- A managed identity: system-assigned, or user-assigned with its client ID in `AZURE_CLIENT_ID`
- The account you signed in with `az login` in the Azure CLI, when using ddup on your local machine

```yaml
providers:
  azure-main:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "my-dns-rg"
      zoneName: "example.com"
      # Optional: the tenant to authenticate with
      #tenantId: "00000000-0000-0000-0000-000000000000"
```

For example, to pass a service principal's credentials as environment variables with Docker:

```sh
docker run -d \
  --name ddup \
  --read-only \
  -v $HOME/.ddup:/etc/ddup:ro \
  -e AZURE_TENANT_ID="00000000-0000-0000-0000-000000000000" \
  -e AZURE_CLIENT_ID="00000000-0000-0000-0000-000000000000" \
  -e AZURE_CLIENT_SECRET="your-client-secret" \
  ghcr.io/italypaleale/ddup:v0
```

At startup, ddup logs which method it's using: `Authenticating to Azure with a service principal`, `with a managed identity`, or `with the default options`.

## Permissions

The identity that ddup uses needs the built-in **DNS Zone Contributor** role on the DNS zone. Assign it on the zone, rather than on the resource group or the subscription, so ddup can change only that zone.

To assign the role to an existing service principal or managed identity:

```sh
az role assignment create \
  --assignee "<client-id>" \
  --role "DNS Zone Contributor" \
  --scope "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Network/dnsZones/<zone-name>"
```

The DNS Zone Contributor role allows changing every record in the zone. To allow only what ddup needs, create a [custom role](https://learn.microsoft.com/azure/role-based-access-control/custom-roles) with these actions, which cover A and AAAA records:

```text
Microsoft.Network/dnsZones/A/read
Microsoft.Network/dnsZones/A/write
Microsoft.Network/dnsZones/A/delete
Microsoft.Network/dnsZones/AAAA/read
Microsoft.Network/dnsZones/AAAA/write
Microsoft.Network/dnsZones/AAAA/delete
```

## Things to know

- The A and AAAA record sets with the domain's name are replaced, including records you created in the Azure portal. See [How ddup manages records](/providers#how-ddup-manages-records).
- Only the global Azure cloud is supported. ddup connects to its Azure Resource Manager endpoint (`management.azure.com`), so zones in national clouds such as Azure Government or Azure China won't work.
