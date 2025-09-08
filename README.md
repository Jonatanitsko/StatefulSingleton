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
- **Smart Pod Placement**: Automatically schedules new pods on different nodes than old pods for better availability
- **Automatic Pod Injection**: Uses webhooks to transparently modify pods without changing your deployment files
- **Tested & Reliable**: Comprehensive unit and end-to-end tests ensure it works as expected

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

#### Easy Way (Recommended)

```bash
# Install everything at once
kubectl apply -k config/default
```

#### Step by Step

```bash
# Install the custom resource definitions
kubectl apply -f config/crd/bases/

# Set up permissions
kubectl apply -f config/rbac/

# Install the webhook
kubectl apply -f config/webhook/

# Deploy the operator
kubectl apply -f config/manager/
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

### What Happens to Your Pods

When you create a StatefulSingleton, the operator automatically modifies matching pods behind the scenes. Here's what gets added:

#### Readiness Control
- Adds a special readiness gate (`apps.statefulsingleton.com/singleton-ready`)
- This prevents the pod from getting traffic until the operator says it's safe

#### Smart Scheduling
- Adds anti-affinity rules so new pods would be scheduled to different nodes than old ones
- Uses hostname-based topology (each node gets preference)
- High priority (weight 100) but not required (so it still works on single-node clusters)

#### Communication Setup
- Creates a shared volume at `/var/run/signal` for the operator to signal when to start
- Mounts wrapper scripts at `/opt/wrapper` that control your app's startup, warpping the original code and entrypoints

#### Helper Container
- Adds a sidecar container called `status-sidecar`
- Uses minimal resources (10m CPU, 32Mi memory)
- Helps manage the startup signals

#### Your App Container
- The webhook replaces your app's startup command with a wrapper script
- Saves your original command in an environment variable
- The wrapper waits for the operator's "go" signal before starting your app

#### Labels for Tracking
- Adds `apps.statefulsingleton.com/managed=true` label
- This is how the operator and anti-affinity rules identify managed pods

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

3. **Wrapper Script Issues**: Check what the wrapper script is doing:
   ```bash
   kubectl logs <pod-name> -c <main-container> | grep StatefulSingleton
   ```

4. **Pod Placement Issues**: See if anti-affinity is working:
   ```bash
   kubectl get pod <pod-name> -o jsonpath='{.spec.affinity.podAntiAffinity}'
   ```

5. **Management Labels Missing**: Make sure the pod got the right labels:
   ```bash
   kubectl get pod <pod-name> --show-labels | grep managed
   ```

## Design Considerations

### Why Not Just Use StatefulSets?

While Kubernetes StatefulSets provide ordered deployment, they don't guarantee that only one instance is running at a time. The StatefulSingleton operator ensures zero parallel execution, which is crucial for applications that cannot run concurrent instances.

### Why Use a Mutating Webhook?

The mutating webhook allows us to transparently modify pods at creation time without requiring users to change their deployment manifests. This makes the solution easier to adopt and more maintainable.

### Termination Grace Period Considerations

The operator respects the termination grace period to allow applications to shut down cleanly. This is essential for stateful applications to flush data, close connections, and perform cleanup operations.

## Development & Testing

### What You Need to Develop

- Go 1.19 or newer
- Docker for building images
- kubectl to talk to your cluster
- Kind for local testing (optional but recommended)
- Ginkgo test framework

### Building the Project

```bash
# Build the operator binary
make build

# Build and push a Docker image
make docker-build docker-push IMG=your-registry/statefulsingleton:latest
```

### Running Tests

#### Unit Tests (Fast)

```bash
# Run all unit tests
make test

# Test just the webhook logic
go test ./internal/webhook/v1/
```

#### End-to-End Tests (Slower but thorough)

```bash
# Install Ginkgo if you don't have it
go install github.com/onsi/ginkgo/v2/ginkgo@latest

# Run all E2E tests (needs a real cluster)
make test-e2e

# Run just the anti-affinity tests
ginkgo -focus "Anti-Affinity" test/e2e/
```

### Local Development

```bash
# Install the CRDs in your cluster
make install

# Run the operator on your machine (points to your current kubectl context)
make run

# In another terminal, try it out
kubectl apply -f config/samples/
```

### What Gets Tested

We have pretty comprehensive tests:

- **Unit Tests**: The webhook logic, anti-affinity rules, pod modifications
- **E2E Tests**: The whole thing working together:
  - Basic functionality (pods start and stop correctly)
  - Rolling updates (old pod stops, new pod starts)
  - Anti-affinity (new pods go to different nodes when possible)
  - Multi-node scenarios
  - What happens when resources are tight
  - Webhook integration
  - Error cases and edge conditions

## How It Actually Works

### The Startup Dance

The operator uses a clever signal system:

1. **Wrapper Takes Over**: Your pod starts with our wrapper script instead of your app
2. **Waiting Game**: The wrapper polls for a "start-signal" file every 10 seconds
3. **Green Light**: When the operator creates the signal file, your app finally starts
4. **Back to Normal**: Your app runs with its original command and arguments

### ConfigMap Magic

The operator manages ConfigMaps with the wrapper scripts:

- **Name**: `statefulsingleton-wrapper` in each namespace
- **Contents**: Shell scripts that do the startup control
- **Lifecycle**: The controller creates and updates these automatically
- **Permissions**: Scripts are executable (chmod 755)

### Anti-Affinity Deep Dive

We automatically add these rules to spread pods across nodes:

```yaml
spec:
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          topologyKey: kubernetes.io/hostname
          labelSelector:
            matchExpressions:
            - key: apps.statefulsingleton.com/managed
              operator: In
              values: ["true"]
```

**Why This Rocks**:
- Better uptime (not all eggs in one basket)
- Spreads load across your cluster
- Rolling updates are smoother
- Less impact when nodes go down

## Contributing

Want to help make this better? Awesome!

### How to Contribute

1. **Add Tests**: New features need tests (both unit and E2E when possible)
2. **Update Docs**: If you add features, update this README
3. **Follow Conventions**: Run `make fmt vet` to keep code clean
4. **Test Everything**: Make sure E2E tests pass for big changes
5. **Don't Break Things**: Keep backward compatibility with existing setups

Feel free to open issues for bugs or feature requests, and pull requests are always welcome!

## License

This project is licensed under the Apache License 2.0 - see the LICENSE file for details.