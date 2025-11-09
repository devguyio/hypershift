# Hacking

## Overview

What do I need to do to test...

* ...changes to `hypershift-operator`

  * [Install the HyperShift in development mode and run the operator locally](#how-to-run-the-hypershift-operator-in-a-local-process); or
  * [Install HyperShift using a custom image](#how-to-install-hypershift-with-a-custom-image)

* ...changes to `control-plane-operator` or any control plane operator

  * [Create a cluster using a custom image](#how-to-create-a-hypershift-guest-cluster-with-a-custom-image)

## Development How-To Guides

### How to run the HyperShift Operator in a local process

#### Prerequisites

- Go 1.22+ installed
- `make` and `gcc` installed (for building)
- `KUBECONFIG` pointing to an OpenShift 4.12+ or compatible Kubernetes management cluster with admin access
- Management cluster either:
  - Has no HyperShift installed yet, OR
  - Has existing HyperShift installation (you'll scale down the operator)
- **Telepresence** installed (required for DNS resolution of cluster-internal `.svc` addresses)

#### Installing Telepresence

The local operator needs to connect to cluster-internal services (e.g., `kube-apiserver.*.svc`) which use Kubernetes DNS. Telepresence routes your local DNS queries through the cluster.

**Install Telepresence:**

On Linux:
```bash
# Download and install
sudo curl -fL https://app.getambassador.io/download/tel2/linux/amd64/latest/telepresence -o /usr/local/bin/telepresence
sudo chmod a+x /usr/local/bin/telepresence
```

On macOS:
```bash
brew install datawire/blackbird/telepresence
```

**Connect to the cluster:**

Before starting the local operator, establish a Telepresence connection:

```bash
telepresence connect
```

You should see output like:
```
Connected to context <your-context> (https://...)
```

**Verify DNS resolution works:**

```bash
# Test that .svc DNS resolution works
nslookup kubernetes.default.svc.cluster.local
# Should return an IP address, not "server can't find"
```

**When finished, disconnect:**

```bash
telepresence quit
```

#### Environment Configuration

For easier management of environment variables, you can use a `.envrc` file:

1. Copy the sample file:
   ```bash
   cp hack/dev/common/envrc.sample .envrc
   ```

2. Edit `.envrc` with your values:
   ```bash
   vim .envrc
   ```

3. Load the environment variables:
   ```bash
   source .envrc
   ```

**Optional: Use direnv for auto-loading**

[direnv](https://direnv.net/) automatically loads `.envrc` when you enter the directory:

```bash
# Install direnv (optional)
# Linux:
curl -sfL https://direnv.net/install.sh | bash

# macOS:
brew install direnv

# Allow direnv to load .envrc:
direnv allow
```

Now environment variables will auto-load when you `cd` into the repository.

#### Steps

**IMPORTANT**: Ensure `KUBECONFIG` environment variable is set to your management cluster:

        $ export KUBECONFIG=/path/to/your/management/cluster/kubeconfig

1. Build HyperShift binaries:

        $ make build

2. Prepare the management cluster:

   **Option A: Clean cluster (no HyperShift installed)**

   Using the make target (recommended - includes OIDC configuration):

        $ make install-hypershift-development

   Or manually:

        $ bin/hypershift install --development \
            --oidc-storage-provider-s3-bucket-name="${BUCKET_NAME}" \
            --oidc-storage-provider-s3-credentials="${AWS_CREDS}" \
            --oidc-storage-provider-s3-region="${REGION}" \
            --private-platform=AWS \
            --aws-private-creds="${AWS_CREDS}" \
            --aws-private-region="${REGION}"

   **Option B: Existing HyperShift installation**

        $ kubectl scale deployment operator -n hypershift --replicas=0

3. Run the HyperShift operator locally with required environment variables and flags:

        $ export MY_NAMESPACE=hypershift
        $ export MY_NAME=operator-local
        $ ./bin/hypershift-operator run \
            --control-plane-operator-image=quay.io/hypershift/hypershift:latest \
            --namespace=hypershift \
            --pod-name=operator-local \
            --metrics-addr=0 \
            --enable-ocp-cluster-monitoring=false

   **Required flags explained:**
   - `--control-plane-operator-image`: Specifies the image to use for control-plane-operator pods
   - `MY_NAMESPACE`/`MY_NAME`: Environment variables used for operator metrics (required but can be any values)
   - `--metrics-addr=0`: Disables metrics server (useful for local development)
   - `--enable-ocp-cluster-monitoring=false`: Disables OCP cluster monitoring integration

   **Platform-specific configuration (AWS example):**

   If you need to create hosted clusters (not just run the operator), add platform credentials:

        $ export HYPERSHIFT_AWS_CREDS="${HOME}/.aws/credentials"
        $ export HYPERSHIFT_REGION="us-east-1"
        $ export HYPERSHIFT_BUCKET_NAME="your-oidc-bucket"
        $ ./bin/hypershift-operator run \
            --control-plane-operator-image=quay.io/hypershift/hypershift:latest \
            --namespace=hypershift \
            --pod-name=operator-local \
            --metrics-addr=0 \
            --enable-ocp-cluster-monitoring=false \
            --private-platform=AWS \
            --oidc-storage-provider-s3-credentials="${HYPERSHIFT_AWS_CREDS}" \
            --oidc-storage-provider-s3-region="${HYPERSHIFT_REGION}" \
            --oidc-storage-provider-s3-bucket-name="${HYPERSHIFT_BUCKET_NAME}"

4. Verify the operator is running:

   You should see log messages indicating:
   - "starting manager"
   - "successfully acquired lease hypershift/hypershift-operator-leader-elect"
   - Controllers starting (hostedcluster, nodepool, etc.)

   Note: The warning "pod not found, reporting empty image" is expected when running locally.

#### Using Make Targets (Recommended)

For easier local development, use the provided make targets:

**Prerequisites:**

1. **Connect Telepresence** (required for DNS resolution):

        $ telepresence connect

2. **Set required environment variables:**

   **Option A: Using .envrc file (Recommended)**
   ```bash
   cp hack/dev/common/envrc.sample .envrc
   vim .envrc  # Edit with your values
   source .envrc
   ```

   **Option B: Export manually**
   ```bash
   export KUBECONFIG=/path/to/your/management/cluster/kubeconfig
   export BUCKET_NAME=your-oidc-bucket-name
   export AWS_CREDS=/path/to/.aws/credentials
   export REGION=us-east-1
   ```

   **Tip:** If you use [direnv](https://direnv.net/), the `.envrc` file will auto-load when entering the directory.

**Workflow:**

1. **Full test workflow** (starts operator, validates, then stops):

        $ make test-local-operator

2. **Start and keep operator running** (for active development):

        $ make start-operator-locally     # Starts operator in background
        $ make validate-local-operator    # Validates it's working
        # Make code changes...
        $ make stop-operator-locally && make start-operator-locally  # Restart

3. **Monitor operator logs**:

        $ tail -f /tmp/hypershift-operator-local-*.log

4. **Stop the operator**:

        $ make stop-operator-locally

**Make targets available:**
- `make install-hypershift-development` - Install HyperShift in development mode with OIDC config
- `make uninstall-hypershift-development` - Uninstall HyperShift development installation
- `make start-operator-locally` - Starts operator in background, logs to `/tmp/`
- `make stop-operator-locally` - Stops the local operator
- `make validate-local-operator` - Validates operator is working (starts if needed)
- `make test-local-operator` - Full cycle: start → validate → stop
- `make create-test-cluster-local` - Creates a test HostedCluster for validation
- `make destroy-test-cluster-local` - Destroys the test cluster
- `make test-local-operator-with-cluster-creation` - Full test including cluster creation

#### Testing Cluster Creation with Local Operator

To verify your local operator can create and reconcile hosted clusters:

1. **Set required environment variables** (or source your .envrc):

        $ export CLUSTER_NAME=my-cluster
        $ export BASE_DOMAIN=example.hypershift.devcluster.openshift.com
        $ export PULL_SECRET=/path/to/pull-secret
        $ export REGION=us-east-1
        $ export AWS_CREDS=/path/to/.aws/credentials
        $ export NAMESPACE=clusters
        $ export REPLICAS=1  # Optional, defaults to 1 for test clusters
        $ export RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.19.18-x86_64  # Optional, defaults to 4.19.18

2. **Create a test cluster**:

        $ make create-test-cluster-local

   This will:
   - Generate manifests to a random tmp directory (e.g., `/tmp/hypershift-test-cluster-abc123/`)
   - Create a timestamped cluster name (e.g., `my-cluster-test-local-1731024567`)
   - Apply manifests to the management cluster
   - Store cluster info in `.test-cluster.info` for cleanup
   - Print manifest location for inspection

3. **Validate operator is reconciling**:

        $ make validate-local-operator

4. **Destroy the test cluster** (to avoid AWS costs):

        $ make destroy-test-cluster-local

   This will run `hypershift destroy` with the saved infraID and preserve manifests for inspection.

5. **Or run the complete test** (create → validate → destroy):

        $ make test-local-operator-with-cluster-creation

**Optional: Test with custom control-plane-operator image:**

        $ export CONTROL_PLANE_OPERATOR_IMAGE=quay.io/myrepo/control-plane-operator:latest
        $ make create-test-cluster-local

### Building Custom Images
To build images that can be both used for HyperShift installation and Hosted Cluster creation

         docker build -f ./Dockerfile.dev --platform=linux/amd64 -t quay.io/blah/hypershift:${TAG} .
         docker push quay.io/blah/hypershift:${TAG}


To build only the HyperShift image for installation

         make IMG=quay.io/my/hypershift:latest docker-build docker-push


To build controlplane-operator image for Hosted Cluster creation

         docker build --platform linux/amd64 -t quay.io/blah/controlplaneoperator:<tag> -f Dockerfile.control-plane .
         docker push  docker push quay.io/blah/controlplaneoperator:<tag>


(Optional) If you need to build a release image containing changes from multiple pull requests you can do so by using Cluster Bot in slack.
   For example to build a 4.18 release image using the below PRs as examples
   * https://github.com/openshift/cluster-storage-operator/pull/522
   * https://github.com/openshift/hypershift/pull/4791

Run the following in Cluster Bot

    build 4.18,openshift/cluster-storage-operator#522,openshift/hypershift#4791

The bot will build a release image and link the job that created it, the image can be found at the bottom of the logs.


### How to install HyperShift with a custom image
1. Install HyperShift using the custom image:

        $ bin/hypershift install --hypershift-image quay.io/my/hypershift:latest

2. (Optional) If your repository is private, create a secret:

        oc create secret generic hypershift-operator-pull-secret  -n hypershift --from-file=.dockerconfig=/my/pull-secret --type=kubernetes.io/dockerconfig

   Then update the operator ServiceAccount in the hypershift namespace:

       oc patch serviceaccount operator -n hypershift -p '{"imagePullSecrets": [{"name": "hypershift-operator-pull-secret"}]}'

### How to create a HyperShift Guest Cluster with a custom image
1. Create a guest cluster using the custom image:

        $ bin/hypershift create cluster openstack --release-image quay.io ...

### How to run the e2e tests

1. Complete [Prerequisites](https://hypershift-docs.netlify.app/getting-started/#prerequisites) with a public Route53
   Hosted Zone, for example with the following environment variables:

   ```shell
   BASE_DOMAIN="my.hypershift.dev"
   BUCKET_NAME="my-oidc-bucket"
   AWS_REGION="us-east-2"
   AWS_CREDS="my/aws-credentials"
   PULL_SECRET="/my/pull-secret"
   HYPERSHIFT_IMAGE="quay.io/my/hypershift:latest"
   ```

2. Install the HyperShift Operator on a cluster, filling in variables such as the S3 bucket name and region based on
   what was done in the prerequisites phase and potentially supplying a custom image.

   ```shell
   $ bin/hypershift install \
       --oidc-storage-provider-s3-bucket-name "${BUCKET_NAME}" \
       --oidc-storage-provider-s3-credentials "${AWS_CREDS}" \
       --oidc-storage-provider-s3-region "${AWS_REGION}" \
       --hypershift-image "${HYPERSHIFT_IMAGE}"
   ```

2. Run the tests.

   ```shell
   $ make e2e
   $ bin/test-e2e -test.v -test.timeout 0 \
       --e2e.aws-credentials-file "${AWS_CREDS}" \
       --e2e.pull-secret-file "${PULL_SECRET}" \
       --e2e.aws-region "${AWS_REGION}" \
       --e2e.availability-zones "${AWS_REGION}a,${AWS_REGION}b,${AWS_REGION}c" \
       --e2e.aws-oidc-s3-bucket-name "${BUCKET_NAME}" \
       --e2e.base-domain "${BASE_DOMAIN}"
   ```

### How to visualize the Go dependency graph

On MacOS, get a nice PDF of the graph:

```
brew install graphviz
go get golang.org/x/exp/cmd/modgraphviz
go mod graph | modgraphviz | dot -T pdf | open -a Preview.app -f
```

### How to update the HyperShift API CRDs

After making changes to types in the `api` package, make sure to update the
associated CRD files:

```shell
$ make api
```

### How to update third-party API types and CRDs

To update third-party API types (e.g. `sigs.k8s.io/cluster-api`), edit the dependency
version in `go.mod` and then update the contents of `vendor`:

```shell
$ go mod vendor
```

Then update the associated CRD files:

```shell
$ make api
```

### How to use go workspaces

Create a directory that will be the parent of the hypershift
code repository:

```shell
$ mkdir hypershift_ws
```

Under that directory, either move an existing hypershift repository or just clone hypershift again

```shell
$ cd hypershift_ws
$ git clone git@github.com:openshift/hypershift
```

Initialize the go workspace

```shell
$ go work init
$ go work use ./hypershift
$ go work use ./hypershift/api
$ go work sync
$ go work vendor
```

Now when running vscode, open the workspace directory to work with hypershift code.
