# StatefulSingleton Operator

A Kubernetes operator for managing stateful singleton applications with controlled transitions and zero parallel execution.

## Overview

The StatefulSingleton operator ensures zero downtime for critical stateful applications that cannot run in parallel by controlling pod transitions during rollouts, node drains, or pod deletions. It works with standard Kubernetes Deployments without requiring modifications to the applications themselves.

## Key Features

- **Zero Parallel Execution**: Guarantees that no two instances of a singleton application can run in parallel
- **Graceful Termination**: Respects configurable termination grace periods for clean shutdowns
- **Minimal Downtime**: Optimizes transition process to minimize gaps between instances
- **Standard Integration**: Works with standard Kubernetes Deployments and StatefulSets
- **No Application Changes**: Operates without requiring modifications to application code

## Architecture and Components

The StatefulSingleton operator architecture consists of several key components working together to ensure controlled transitions between pods:

```
┌─────────────────────────────────────────────────────────────────────┐
│                       OpenShift Cluster                              │
│                                                                      │
│  ┌───────────────────────┐      ┌───────────────────────────────┐   │
│  │ StatefulSingleton CR  │      │ Kubernetes API                │   │
│  │                       │      │                               │   │
│  │ - Selector            │      │                               │   │
│  │ - Max Transition Time │      │                               │   │
│  └───────────┬───────────┘      └───────┬───────────────────────┘   │
│              │                          │                           │
│              │                          │                           │
│              ▼                          ▼                           │
│  ┌───────────────────────┐      ┌───────────────────────────────┐   │
│  │ Operator Controller   │      │ Mutating Webhook              │   │
│  │                       │◄─────┤                               │   │
│  │ - Watches Pods        │      │ - Intercepts Pod Creation     │   │
│  │ - Manages Transitions │      │ - Injects Wrapper Script      │   │
│  │ - Updates Status      │      │ - Adds Readiness Gate         │   │
│  └───────────┬───────────┘      └───────────────────────────────┘   │
│              │                                                       │
│              │                                                       │
│              ▼                                                       │
│  ┌───────────────────────────────────────────────┐                  │
│  │ Pod Management                                 │                  │
│  │                                                │                  │
│  │  ┌────────────────┐        ┌────────────────┐ │                  │
│  │  │ Old Pod        │        │ New Pod        │ │                  │
│  │  │                │        │                │ │                  │
│  │  │ ┌────────────┐ │        │ ┌────────────┐ │ │                  │
│  │  │ │ Main App   │ │        │ │ Main App   │ │ │                  │
│  │  │ │ (Running)  │ │        │ │ (Blocked)  │ │ │                  │
│  │  │ └────────────┘ │        │ └────────────┘ │ │                  │
│  │  │                │        │                │ │                  │
│  │  │ ┌────────────┐ │        │ ┌────────────┐ │ │                  │
│  │  │ │ Sidecar    │ │        │ │ Sidecar    │ │ │                  │
│  │  │ │ Container  │ │        │ │ Container  │ │ │                  │
│  │  │ └────────────┘ │        │ └────────────┘ │ │                  │
│  │  │                │        │                │ │                  │
│  │  │ Signal: Active │◄───────┤ Signal: Waiting│ │                  │
│  │  └────────────────┘        └────────────────┘ │                  │
│  │                                                │                  │
│  │  ┌────────────────────────────────────────┐   │                  │
│  │  │ Shared Storage (PVC ReadWriteMany)     │   │                  │
│  │  └────────────────────────────────────────┘   │                  │
│  └───────────────────────────────────────────────┘                  │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

### Components

1. **StatefulSingleton Custom Resource**: Defines the parameters for controlling transitions
2. **Mutating Webhook**: Intercepts pod creation to inject process control mechanisms
3. **Controller**: Manages the transition process between old and new pods
4. **ConfigMap**: Contains the wrapper script used to control application startup
5. **Sidecar Container**: Facilitates communication between the operator and application containers

## Transition Flow and Operator Logic

The StatefulSingleton operator follows a strict process to ensure zero parallel execution:

1. **Initialization**:
   - User creates a Deployment/StatefulSet and a StatefulSingleton resource
   - Initial pod is created and allowed to start normally

2. **New Pod Creation**:
   - When a new pod is created (rollout, node drain, etc.), the webhook intercepts it
   - The webhook injects a wrapper script that blocks the main application process
   - The pod starts but its main process remains blocked

3. **Transition Management**:
   - The controller detects the new pod and keeps it in a non-ready state
   - The controller monitors the old pod for termination
   - When the old pod begins terminating, the controller respects its grace period

4. **Handover**:
   - After the old pod is completely terminated, the controller signals the new pod
   - The new pod's wrapper script detects the signal and starts the main application
   - The controller updates the pod's readiness condition to allow traffic

5. **Completion**:
   - The new pod becomes the active instance
   - The StatefulSingleton resource status is updated to reflect the new active pod

This flow ensures there is never a moment when both pods are running their main processes simultaneously, making it perfect for stateful applications that cannot operate in parallel.

## Getting Started

### Prerequisites

- OpenShift 4.12+ or Kubernetes 1.22+
- kubectl/oc CLI tools
- Storage class with ReadWriteMany capability

### Installation

```bash
# Install CRDs
kubectl apply -f config/crd/bases/apps.openshift.statefulsingleton.com_statefulsingleton.yaml

# Install RBAC
kubectl apply -f config/rbac/

# Deploy the operator
kubectl apply -f config/manager/manager.yaml
```

### Usage

1. Create a StatefulSingleton resource:

```yaml
apiVersion: apps.openshift.statefulsingleton.com/v1
kind: StatefulSingleton
metadata:
  name: my-database
  namespace: production
spec:
  selector:
    matchLabels:
      app: postgres-database
      tier: database
  terminationGracePeriod: 120  # 2 minutes
  maxTransitionTime: 300       # 5 minutes
```

2. Create a standard Deployment with matching labels:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres-database
  namespace: production
spec:
  replicas: 1
  selector:
    matchLabels:
      app: postgres-database
      tier: database
  template:
    metadata:
      labels:
        app: postgres-database
        tier: database
    spec:
      containers:
      - name: postgres
        image: postgres:14
        # ... regular container configuration
```

3. The operator will now manage transitions between pods to ensure zero parallel execution.

## Configuration Options

The StatefulSingleton CRD supports the following configuration options:

| Field | Description | Default |
|-------|-------------|---------|
| `spec.selector` | Label selector for pods to manage | Required |
| `spec.terminationGracePeriod` | Grace period (seconds) for pod termination | 30 |
| `spec.maxTransitionTime` | Maximum time (seconds) for a transition | 300 |
| `spec.respectPodGracePeriod` | Whether to use pod's grace period if longer | true |

## Monitoring and Troubleshooting

### Checking Status

```bash
# Check StatefulSingleton status
kubectl get statefulsingleton my-database

# Get detailed status
kubectl describe statefulsingleton my-database
```

### Checking Logs

```bash
# View operator logs
kubectl logs -f deployment/statefulsingleton-controller-manager -c manager
```

### Common Issues

1. **Stuck Transitions**: If a transition is stuck, check:
   - Pod termination status: `kubectl get pod <pod-name> -o yaml`
   - Controller logs for errors
   - Status of the readiness gate: `kubectl get pod <pod-name> -o jsonpath='{.status.conditions}'`

2. **Signal File Issues**: Check if the signal file exists:
   ```bash
   kubectl exec -it <pod-name> -c status-sidecar -- ls -la /var/run/signal/
   ```

3. **Wrapper Script Issues**: Inspect the wrapper script log:
   ```bash
   kubectl logs <pod-name> -c <main-container> | grep StatefulSingleton
   ```

## Design Considerations

### Why Not Just Use StatefulSets?

While Kubernetes StatefulSets provide ordered deployment, they don't guarantee that only one instance is running at a time. The StatefulSingleton operator ensures zero parallel execution, which is crucial for applications that cannot run concurrent instances.

### Why Use a Mutating Webhook?

The mutating webhook allows us to transparently modify pods at creation time without requiring users to change their deployment manifests. This makes the solution easier to adopt and more maintainable.

### Termination Grace Period Considerations

The operator respects the termination grace period to allow applications to shut down cleanly. This is essential for stateful applications to flush data, close connections, and perform cleanup operations.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the Apache License 2.0 - see the LICENSE file for details.