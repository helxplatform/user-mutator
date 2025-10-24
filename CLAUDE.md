# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Kubernetes Mutating Admission Webhook that dynamically modifies Kubernetes Deployment resources for user-specific customization. The webhook intercepts deployment creation requests and injects user-specific volumes, volume mounts, security contexts, environment variables, and LDAP configurations based on the username label.

**Primary Language**: Go 1.23
**Key Dependencies**: kubernetes client-go, gorilla/mux, go-ldap

## Build and Development Commands

### Building
```bash
# Build Docker image (multi-architecture)
make build

# Build Go binary locally
make go-build

# Run tests
make go-test
```

### Deployment

The project uses Helm for Kubernetes deployment and requires TLS certificates for the webhook:

```bash
# Generate TLS certificates and keys
make ca-key-cert

# Create Kubernetes secret with certificates
make key-cert-secret

# Deploy webhook server via Helm
make deploy-webhook-server

# Create MutatingWebhookConfiguration
make mutate-config

# Deploy everything (certificates + webhook + config)
make deploy-all

# Enable mutation in a specific namespace (set NAMESPACE_TO_MUTATE in config.env)
make enable-mutate-in-namespace

# Clean up all resources
make clean
```

### Local Development with Kind

```bash
# Create local Kind cluster
make kind-up

# Build and load image into Kind
make kind-load

# Deploy everything to Kind cluster
make kind-all

# Tear down Kind cluster
make kind-down
```

### Configuration

Edit `config.env` to customize:
- `VERSION`: Application version
- `WEBHOOK_NAMESPACE`: Namespace where webhook runs
- `NAMESPACE_TO_MUTATE`: Namespace to intercept deployments
- `MUTATE_CONFIG`: Name of the MutatingWebhookConfiguration
- `SECRET`: Name of TLS secret

## Architecture

### Main Components

**webhook-server/** - Main webhook admission controller
- Single Go application (`main.go`, ~1560 lines)
- HTTP server listening on port 8443 with TLS
- Endpoints:
  - `/mutate` - Admission review handler
  - `/readyz` - Readiness probe
  - `/healthz` - Liveness probe

**tls-and-mwc/** - TLS certificate generation and webhook registration
- `generateTLSCerts.go` - Creates CA and server certificates
- `createMutationConfig.go` - Registers MutatingWebhookConfiguration in cluster
- Run with `go run main.go createMutationConfig.go generateTLSCerts.go [-M]`

**chart/** - Helm chart for deployment
- Deploys webhook server as Kubernetes Deployment
- Creates Service, ServiceAccount, RBAC resources
- Mounts TLS certificates and configuration

### Core Workflow

1. **Admission Request**: Kubernetes API sends AdmissionReview when a Deployment is created
2. **User Extraction**: Webhook extracts `username` label from Deployment metadata
3. **Profile Loading**: Loads user-specific configuration from `/etc/user-mutator-maps/user-profiles/{username}.yaml` and `auto.yaml`
4. **LDAP Lookup** (optional): Fetches user attributes (UID, GID, groups, supplementalGroups, posixGroups) from LDAP server
5. **Resource Construction**:
   - **Volumes**: Parses volume sources with template expansion and regex matching
     - Supports `pvc://`, `secret://`, `nfs://` schemes
     - Regex-based fan-out: one VolumeSource can match multiple PVCs/Secrets
     - Template variables: `{{.username}}`, `{{.cap}}` (capture groups), `{{.Index}}`
   - **Volume Mounts**: Associates volume specs with concrete volumes using templated paths
   - **Security Contexts**: Sets runAsUser, runAsGroup, fsGroup, supplementalGroups
   - **Env Variables**: Injects environment variables from user profiles and LDAP
   - **Group Volumes**: Auto-mounts PVCs labeled with user's group names
   - **LDAP Config**: Mounts ConfigMap with libnss-ldap.conf for user/group resolution
6. **Patch Calculation**: Creates JSONPatch to modify the original Deployment
7. **Response**: Returns AdmissionResponse with patch to Kubernetes API

### Key Functions (webhook-server/main.go)

- `handleAdmissionReview` (1471): HTTP handler for admission requests
- `processAdmissionReview` (1383): Main admission logic dispatcher
- `appendProfiles` (1316): Loads and expands user profiles with volume fan-out
- `GetVolumeContextsMap` (758): Parses volume sources and performs regex-based resource discovery
- `parseVolumeSources` (643): Expands single VolumeSource into multiple VolumeContexts (fan-out)
- `FindMatchingResources` (583): Lists PVCs/Secrets in namespace and applies hybrid regex
- `compileHybridRegex` (525): Template expansion + regex compilation (literal text + capture groups)
- `GetK8sVolumeMounts` (835): Builds VolumeMounts with template-based paths and cardinality validation
- `calculatePatch` (1261): Generates JSONPatch from original vs modified Deployment
- `searchLDAP` (355): Queries LDAP for user attributes and groups
- `constructSecurityContexts` (969): Builds Pod and Container SecurityContexts from user data
- `getVolumesAndMountsForUserGroups` (1066): Auto-discovers group PVCs by label

### Volume Source Regex and Fan-Out

The system supports **regex-based fan-out** where a single `VolumeSource` can match multiple cluster resources:

**Hybrid Regex Syntax**:
- Template variables: `{{.username}}`
- Literal text: automatically escaped
- Capture groups: `(...)` - only parenthesized content treated as regex

**Example**: `pvc://{{.username}}-data-(.*)` matches all PVCs like `alice-data-project1`, `alice-data-project2`

**Cardinality Rules** (enforced in `buildMountPairs`):
- **N:N** - N volume matches, N mount specs → pair by index
- **1:N** - 1 volume match, N mount specs → replicate volume
- **N:1** - N volume matches, 1 mount spec → replicate mount with templated paths
- **Unsupported** - Different N and M (not 1) → error, all mounts for that name skipped

**Mount Path Templating**: Use `.cap`, `.Index`, `.ResourceName` in mount paths:
```yaml
volumeMounts:
  - name: project-data
    mountPath: /mnt/projects/{{index .cap 0}}  # uses first capture group
```

### Configuration Structure

**Runtime Configuration** (`/etc/user-mutator-config/config.json`):
```json
{
  "meta": {"libnss_ldap_config_map_name": "ldap-config"},
  "features": {
    "ldap": {
      "host": "ldap.example.com",
      "port": 389,
      "username": "cn=admin,dc=example,dc=com",
      "user_base_dn": "ou=users,dc=example,dc=com",
      "group_base_dn": "ou=groups,dc=example,dc=com"
    }
  },
  "secrets": {"cert": "user-mutator-cert-tls"}
}
```

**User Profile** (`/etc/user-mutator-maps/user-profiles/{username}.yaml`):
```yaml
volumes:
  volumeSources:
    - name: home
      source: "pvc://{{.username}}-home"
    - name: shared
      source: "nfs://nfs-server:/exports/{{.username}}"
  volumeMounts:
    - name: home
      mountPath: "/home/{{.username}}"
    - name: shared
      mountPath: "/shared"
secretsFrom:
  - secretName: user-credentials
```

### LDAP Integration

When LDAP is configured:
- Searches for user by `uid` attribute
- Fetches posixAccount attributes: `uidNumber`, `gidNumber`, `homeDirectory`
- Fetches user groups from `memberOf` attribute
- Searches for posixGroups where user is a `memberUid`
- Supports user aliases via `userAlias` attribute (merges attributes from alias user)
- Sets security contexts based on LDAP numeric IDs
- Mounts libnss-ldap configuration for runtime user/group resolution

### Testing and Debugging

The webhook includes extensive debug logging (use `slog.LevelDebug` in main):
- `printVolumes`: Logs all volumes being added
- `printVolumeMounts`: Logs all volume mounts being added
- `printPatchOperations`: Logs JSONPatch operations

Set log level in `webhook-server/main.go:1533`:
```go
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug, // Change to Debug for verbose logs
}))
```

View webhook logs:
```bash
kubectl -n <WEBHOOK_NAMESPACE> logs -l app.kubernetes.io/name=user-mutator -f
```

### Helper Tools (tools/ directory)

Python scripts for setup and testing:
- `create_webhook_config.py` - Alternative webhook registration (Python-based)
- `generate_testdata.py` - Creates test user profile YAML files
- `create_ldap_password.py` - Creates Kubernetes secret with LDAP password
- `generate_configmap.py` - Creates ConfigMap with libnss-ldap configuration

## Common Development Patterns

### Adding a New Volume Source Type

1. Add case in `parseVolumeSources` switch (line 643)
2. Implement resource discovery logic (similar to PVC/Secret cases)
3. Return slice of `VolumeContext` with unique volume names and vars map
4. Update documentation for new scheme syntax

### Modifying User Profile Schema

1. Update `UserProfiles` struct (line 90)
2. Update profile loading in `ReadUserProfilesFromFile` (line 313)
3. Update `appendProfiles` to handle new fields (line 1316)
4. Regenerate test data with `tools/generate_testdata.py`

### Adding New LDAP Attributes

1. Add field to `User` struct (line 102)
2. Add attribute name to `searchLDAP` SearchRequest (line 375)
3. Map attribute in `searchLDAP` result parsing (line 416)
4. Use attribute in `constructSecurityContexts` or `constructEnv` as needed
