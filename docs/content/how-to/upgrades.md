# Upgrades
HyperShift enables the decoupling of upgrades between the Control Plane and Nodes.

This allows there to be two separate procedures a [Cluster Service Provider](../reference/concepts-and-personas.md#personas) can take, giving them flexibility to manage the different components separately.

Control Plane upgrades are driven by the HostedCluster, while Node upgrades are driven by its respective NodePool. Both the HostedCluster and NodePool expose a `.spec.release` field where the OCP release image can be specified.

For a cluster to remain fully operational during an upgrade process, Control Plane and Node upgrades need to be orchestrated while satisfying [Kubernetes version skew policy](https://kubernetes.io/releases/version-skew-policy/) at any time. The supported OCP versions are dictated by the running HyperShift Operator [see here](../reference/versioning-support.md) for more details on versioning.

## HostedCluster
`.spec.release` dictates the version of the Control Plane.

The HostedCluster propagates the intended `.spec.release` to the `HostedControlPlane.spec.release` and runs the appropriate Control Plane Operator version.

The upgrade is carried out by two operators working on different sides:

- The **Control Plane Operator (CPO)** deploys management-side control plane components into the HCP namespace on the management cluster. These are represented by `ControlPlaneComponent` custom resources and include components like kube-apiserver, etcd, kube-controller-manager, kube-scheduler, and openshift-apiserver.
- The **[Cluster Version Operator (CVO)](https://github.com/openshift/cluster-version-operator)** also runs in the HCP namespace on the management cluster but manages the data-plane rollout -applying the OCP payload to the hosted cluster for data-plane components like OVN pods and console. Their rollout depends on NodePool compute availability.

Traditionally, in standalone OCP, the CVO has been the sole source of truth for upgrades. In HyperShift, the responsibility is split between CPO and CVO. This split enabled the flexibility and speed the HyperShift project needed for the CPO to support the management/guest cluster topology and the multiple customizations needed for manifests that otherwise would have needed to be segregated in the payload.

HyperShift exposes available upgrades in `HostedCluster.Status` by bubbling up the status of the `ClusterVersion` resource inside the hosted cluster. While HCP is authoritative over upgrades, it does honour the CVO's `ClusterVersionUpgradeable` condition at a higher layer: z-stream upgrades are allowed regardless, but y-stream upgrades are blocked unless overridden via the `hypershift.openshift.io/force-upgrade-to` annotation on the HostedCluster. Some of the builtin CVO features like recommendations and allowed upgrade paths are not directly surfaced, but the underlying information is still available in the `HostedCluster.Status` field for consumers to read.

### Understanding Version Tracking: Control Plane vs Data Plane

When `.spec.release` is updated on a HostedCluster, two separate rollouts happen on two different sides:

- **Control plane (CP)** -management-side components represented by `ControlPlaneComponent` resources in the HCP namespace (kube-apiserver, etcd, kube-controller-manager, kube-scheduler, openshift-apiserver, etc.). Deployed by the CPO. These typically complete first.
- **Data plane (DP)** -workloads running on guest cluster worker nodes (OVN pods, console, etc.). Managed by the CVO (which itself runs in the HCP namespace). Their rollout depends on NodePool compute availability.

These rollouts complete independently. The control plane can finish while the data plane is still progressing -or may even complete when no worker nodes exist at all (zero-compute clusters). Tracking them separately enables service providers to confirm CVE patches are applied to management-side components, track upgrade completion for fleet-management decisions, and compute NodePool version skew -all without waiting for the data-plane rollout to finish.

!!! warning "Known limitation"

    Some management-side components (notably OVN) currently have data-plane dependencies that can block the control plane version from reaching `Completed`. This means `controlPlaneVersion` may remain `Partial` due to data-plane issues even though the control plane components themselves are ready. This limitation is being actively resolved -each component is responsible for removing its own data-plane dependencies so that its management-side rollout can complete independently.

When `ControlPlaneReleaseImage` is set on the HostedControlPlane spec, the control plane and data plane may use different release images. In this case, `controlPlaneVersion.desired` and `version.desired` will show different versions -this is expected and reflects the intentional split.

HyperShift tracks each rollout separately in the HostedCluster status:

| Status field | Tracks | Managed by | Shown as |
|---|---|---|---|
| `status.version` | Data-plane rollout | CVO | `VERSION` and `PROGRESS` columns |
| `status.controlPlaneVersion` | Control-plane rollout | CPO | `CP VERSION` column |

Both fields contain a `history` list where the newest entry is first, with state `Completed` or `Partial`.

### Control Plane Version Status

The `status.controlPlaneVersion` field exists on both `HostedCluster` and `HostedControlPlane`.

!!! note

    This field is populated by the control plane operator once it begins reconciling. It will be absent from the resource status on newly created clusters until the first reconciliation completes, and on clusters managed by older operator versions that do not support this field.

The field contains:

- **`desired`** -a release object describing what the control plane is reconciling towards. It contains:
    - `version`: the semantic version string (e.g. "4.18.5").
    - `image`: the release image pullspec.
- **`history`** -a list of version transitions (newest first, minimum 1 entry when present, capped at 100). Each entry has:
    - `state`: `Completed` (all `ControlPlaneComponent` resources report the target version) or `Partial` (rollout not yet fully applied).
    - `startedTime`: when the rollout started.
    - `completionTime`: when the rollout finished (only set for `Completed` entries).
    - `version`: the semantic version string (e.g. "4.18.5").
    - `image`: the release image pullspec.
- **`observedGeneration`** -reports which generation of the `HostedControlPlane` spec is being synced. On the `HostedCluster`, this value is propagated from the underlying `HostedControlPlane`.

#### Understanding the `Partial` State

A `Partial` state means the rollout has not yet fully completed -one or more `ControlPlaneComponent` resources have not reached the target version. This can indicate either an in-progress rollout or a stalled one. To distinguish the two:

- Check the `HostedControlPlane` conditions (e.g. `Progressing`, `Degraded`) for signals about whether the rollout is still making progress or has encountered errors.
- A rollout that remains `Partial` for an extended period without progress in component conditions likely requires operator investigation.

If `.spec.release` is changed back to a previous version (rollback), a new history entry is prepended for the rollback target, and the previously `Partial` entry remains in history.

### Monitoring Upgrade Progress

The `HostedCluster` printer columns surface both CP and DP version status:

```
oc get --namespace clusters hostedclusters
NAME    VERSION   CP VERSION   KUBECONFIG                 PROGRESS    AVAILABLE   PROGRESSING   MESSAGE
my-hc   4.18.5    4.18.5       my-hc-admin-kubeconfig     Completed   True        False         The hosted control plane is available
```

| Column | What it shows |
|---|---|
| `VERSION` | Most recent completed data-plane version (from CVO) |
| `CP VERSION` | Most recent completed control-plane version (from CPO) |
| `PROGRESS` | Data-plane rollout state (latest entry) |

During an in-progress upgrade, `CP VERSION` may already show the new version while `VERSION` still reflects the previous one -this is normal and means the control plane finished first.

On newly created clusters, both `CP VERSION` and `VERSION` will be blank until their respective rollouts complete. The `Available` condition can be `True` before version columns are populated -`Available` reflects API server readiness, not full rollout completion.

With `-o wide`, two additional columns appear:

- `CP Progress` (Control Plane): the state of the latest control-plane history entry.
- `DP Progress` (Data Plane): the state of the latest data-plane history entry.

### Use Cases

- **CVE verification**: Confirm that a security patch has been applied to all management-side control plane components by checking `status.controlPlaneVersion.history[0].state == "Completed"`, without waiting for the full data-plane rollout to finish.
- **Upgrade completion tracking**: Track control plane upgrade completion independently from the data plane, enabling fleet-management decisions like marking y-stream end-of-support or z-stream forced upgrades as done.
- **Zero-compute clusters**: Upgrade a HostedCluster control plane even when no NodePools exist or all are scaled to zero -in this scenario, the CVO cannot complete its rollout but the control plane version status still reflects the management-side rollout.
- **NodePool version skew computation**: Use the control plane version history to determine which NodePool versions are allowed under the [version skew policy](../reference/versioning-support.md#hostedcluster-and-nodepool-version-compatibility), including during multi-step or failed upgrades where multiple versions may be in flight.

See [NodePool Lifecycle](./automated-machine-management/nodepool-lifecycle.md) for details on triggering NodePool upgrades.

## NodePools
`.spec.release` dictates the version of any particular NodePool.

A NodePool will perform a Replace/InPlace rolling upgrade according to `.spec.management.upgradeType`. See [NodePool Rollouts](../reference/nodepool-rollouts.md) for details on what triggers a rollout and how it is executed.
