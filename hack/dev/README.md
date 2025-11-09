# HyperShift Development Scripts

This directory contains scripts for local HyperShift operator development and testing.

## Directory Structure

```
hack/dev/
├── common/          # Shared utilities and templates
│   ├── envrc.sample
│   ├── stop-operator.sh
│   ├── uninstall-hypershift.sh
│   └── validate-local-operator.sh
└── aws/             # AWS-specific scripts
    ├── install-hypershift.sh
    ├── start-operator.sh
    ├── create-cluster.sh
    └── destroy-cluster.sh
```

## Common Scripts and Templates

This directory contains shared utilities and configuration templates used across all platforms.

### envrc.sample
Environment variable template for local development.

**What it does:** Provides a template for setting up required environment variables.

**Usage:**
```bash
cp hack/dev/common/envrc.sample .envrc
vim .envrc  # Edit with your values
source .envrc  # Or use direnv for automatic loading
```

### uninstall-hypershift.sh
Uninstalls HyperShift development installation.

**What it does:** Reads the installation manifest tracker and uninstalls HyperShift resources.

**Usage:**
```bash
./hack/dev/common/uninstall-hypershift.sh [--force]
```

**Environment Variables:**
- `INSTALL_MANIFEST_TRACKER` - Tracker file location (default: .hypershift-install.yaml)

### stop-operator.sh
Stops the local HyperShift operator.

**What it does:** Terminates the locally running operator process using the PID file.

**Usage:**
```bash
./hack/dev/common/stop-operator.sh [--force]
```

**Environment Variables:**
- `PID_FILE` - PID file location (default: .local-operator.pid)

### validate-local-operator.sh
Validates that the local operator is running correctly.

**What it does:** Checks operator process, lease acquisition, and basic functionality.

**Usage:**
```bash
./hack/dev/common/validate-local-operator.sh
```

## AWS-Specific Scripts

### aws/install-hypershift.sh
Installs HyperShift with AWS OIDC configuration.

**Usage:**
```bash
./hack/dev/aws/install-hypershift.sh
```

**Environment Variables:**
- `KUBECONFIG`, `BUCKET_NAME`, `AWS_CREDS`, `REGION` (all required)

### aws/start-operator.sh
Starts operator with AWS-specific flags and configuration.

**Usage:**
```bash
./hack/dev/aws/start-operator.sh [--control-plane-operator-image IMAGE]
```

### aws/create-cluster.sh
Creates a test HostedCluster for operator validation.

**Usage:**
```bash
./hack/dev/aws/create-cluster.sh [OPTIONS]

Options:
  --cluster-name NAME              Cluster name (default: from CLUSTER_NAME env var)
  --namespace NAMESPACE            Namespace (default: clusters)
  --replicas COUNT                 Node pool replicas (default: 1)
  --release-image IMAGE            OpenShift release image
  --control-plane-operator-image   Custom CPO image
  --help                           Show help
```

**Environment Variables:**
- `CLUSTER_NAME`, `BASE_DOMAIN`, `PULL_SECRET`, `REGION`, `AWS_CREDS` (all required)
- `NAMESPACE`, `REPLICAS`, `RELEASE_IMAGE` (optional)

### aws/destroy-cluster.sh
Destroys test HostedCluster and cleans up AWS resources.

**Usage:**
```bash
./hack/dev/aws/destroy-cluster.sh [--force]
```

## Quick Start

1. **Set up environment:**
   ```bash
   cp hack/dev/common/envrc.sample .envrc
   vim .envrc  # Edit with your values
   source .envrc
   ```

2. **Run via Make targets (recommended):**
   ```bash
   make install-hypershift-development
   make start-operator-locally
   make validate-local-operator
   ```

3. **Or run AWS scripts directly:**
   ```bash
   ./hack/dev/aws/install-hypershift.sh
   ./hack/dev/aws/start-operator.sh
   ./hack/dev/common/validate-local-operator.sh
   ```

## Script Design Principles

All scripts follow these conventions:
- Support `--help` flag for usage information
- Validate required environment variables
- Return proper exit codes (0=success, 1=error, 2=usage)
- Use `set -euo pipefail` for safety
- Provide clear output with ✓/✗/⚠ prefixes
- Are idempotent (safe to run multiple times)

## Adding New Platforms

To add support for a new platform (e.g., Azure):

1. Create `hack/dev/azure/` directory
2. Implement platform-specific scripts:
   - `azure/install-hypershift.sh`
   - `azure/start-operator.sh`
   - `azure/create-cluster.sh` (optional)
   - `azure/destroy-cluster.sh` (optional)
3. Add new Makefile variables for Azure scripts
4. Update Makefile targets to support Azure
5. Update documentation

## See Also

- [HACKING.md](../../HACKING.md) - Comprehensive development guide
- [docs/content/contribute/run-hypershift-operator-locally.md](../../docs/content/contribute/run-hypershift-operator-locally.md) - Public documentation
