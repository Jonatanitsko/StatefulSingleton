# Overview
The StatefulSingleton operator ensures zero downtime for critical stateful applications that cannot run in parallel by controlling pod transitions during rollouts, node drains, or pod deletions. It works with Kubernetes Deployment object without requiring modifications to the applications themselves.

# Architecture and Components
The StatefulSingleton operator architecture consists of several key components working together to ensure controlled transitions between pods:
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
Transition Flow
The diagram above illustrates the architecture of the StatefulSingleton operator. Here's how the components work together during a transition:

The StatefulSingleton Controller continuously watches both the StatefulSingleton CR and pods that match the selector
When a new pod is created (by Deployment controller, StatefulSet controller, etc.):

The Mutating Webhook intercepts the pod creation request
It adds the wrapper script, readiness gate, and sidecar container
The pod is created with these modifications


The StatefulSingleton Controller detects the new pod and:

Keeps the new pod's readiness gate in a "not ready" state
Monitors the old pod for termination
When the old pod begins terminating, it creates the signal file in the new pod
The main application in the new pod then starts


The transition is complete when the old pod terminates and the new pod is running

This flow ensures that there's never a moment when both pods are running their main processes simultaneously, while still allowing for rapid pod creation and volume mounting.
Components Detailed
1. Custom Resource Definition (CRD)
The StatefulSingleton CRD defines the parameters for controlling pod transitions:
yamlapiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: statefulsingleton.apps.openshift.yourdomain.com
spec:
  group: apps.openshift.yourdomain.com
  names:
    kind: StatefulSingleton
    listKind: StatefulSingletonList
    plural: statefulsingleton
    singular: statefulsingleton
    shortNames:
    - stfs
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            required:
            - selector
            properties:
              selector:
                type: object
                properties:
                  matchLabels:
                    type: object
                    additionalProperties:
                      type: string
              maxTransitionTime:
                type: integer
                description: "Maximum time in seconds to wait for a transition"
                default: 300
              terminationGracePeriod:
                type: integer
                description: "Time in seconds to wait for the pod to terminate"
                default: 30
          status:
            type: object
            properties:
              activePod:
                type: string
              phase:
                type: string
                enum: ["Running", "Transitioning", "Failed"]
              transitionTimestamp:
                type: string
                format: date-time
              message:
                type: string
    additionalPrinterColumns:
    - name: Status
      type: string
      jsonPath: .status.phase
    - name: Active Pod
      type: string
      jsonPath: .status.activePod
    - name: Age
      type: date
      jsonPath: .metadata.creationTimestamp
    subresources:
      status: {}
2. Mutating Webhook
The webhook intercepts pod creation requests and modifies them to include our custom components:
yamlapiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: statefulsingleton-mutating-webhook-configuration
webhooks:
- name: pod-mutation.openshift.yourdomain.com
  admissionReviewVersions: ["v1", "v1beta1"]
  clientConfig:
    service:
      name: statefulsingleton-webhook-service
      namespace: statefulsingleton-operator-system
      path: "/mutate-v1-pod"
  rules:
  - apiGroups: [""]
    apiVersions: ["v1"]
    operations: ["CREATE"]
    resources: ["pods"]
    scope: "Namespaced"
  sideEffects: None
  timeoutSeconds: 5
  failurePolicy: Ignore
3. Controller Manager
The controller manager reconciles StatefulSingleton resources and manages pod transitions:
yamlapiVersion: apps/v1
kind: Deployment
metadata:
  name: statefulsingleton-controller-manager
  namespace: statefulsingleton-operator-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: statefulsingleton-controller-manager
  template:
    metadata:
      labels:
        app: statefulsingleton-controller-manager
    spec:
      containers:
      - name: manager
        image: quay.io/yourdomain/statefulsingleton-operator:v0.1.0
        args:
        - "--leader-elect"
        - "--metrics-bind-address=:8080"
        - "--health-probe-bind-address=:8081"
        resources:
          limits:
            cpu: 500m
            memory: 256Mi
          requests:
            cpu: 100m
            memory: 128Mi
      serviceAccountName: statefulsingleton-controller-manager
4. ConfigMap for Wrapper Script
The wrapper script that's injected into containers:
yamlapiVersion: v1
kind: ConfigMap
metadata:
  name: statefulsingleton-wrapper
  namespace: my-project
data:
  entrypoint-wrapper.sh: |
    #!/bin/sh
    set -e

    echo "StatefulSingleton: Container starting, waiting for signal before executing application..."

    # Extract original entrypoint from env variable
    if [ -n "$ORIGINAL_ENTRYPOINT" ]; then
      ENTRYPOINT_JSON="$ORIGINAL_ENTRYPOINT"
    else
      # Default empty entrypoint
      ENTRYPOINT_JSON='{"command":[],"args":[]}'
    fi

    # Parse JSON to extract command and args
    COMMAND=$(echo "$ENTRYPOINT_JSON" | grep -o '"command":\[[^]]*\]' | sed 's/"command":\[//;s/\]//' | tr -d '"' | tr ',' ' ')
    ARGS=$(echo "$ENTRYPOINT_JSON" | grep -o '"args":\[[^]]*\]' | sed 's/"args":\[//;s/\]//' | tr -d '"' | tr ',' ' ')

    # Wait for signal file
    while [ ! -f /var/run/signal/start-signal ]; do
      sleep 1
    done

    echo "StatefulSingleton: Start signal received, executing application: $COMMAND $ARGS"

    # If command is empty, run the args directly
    if [ -z "$COMMAND" ]; then
      if [ -z "$ARGS" ]; then
        # No command or args, try to find the image's entrypoint
        echo "StatefulSingleton: No command or args specified, running default entrypoint"
        exec /bin/sh
      else
        # Run args as a command
        exec $ARGS
      fi
    else
      # Run command with args
      exec $COMMAND $ARGS
    fi
Sample Implementation Files
Let's provide some additional sample files for reference:
Sample StatefulSingleton Resource
yamlapiVersion: apps.openshift.yourdomain.com/v1
kind: StatefulSingleton
metadata:
  name: my-database
  namespace: production
spec:
  selector:
    matchLabels:
      app: postgres-database
      tier: database
  maxTransitionTime: 600  # 10 minutes
  terminationGracePeriod: 120  # 2 minutes
Sample Deployment for a Stateful Application
yamlapiVersion: apps/v1
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
  strategy:
    type: Recreate  # Important for stateful applications
  template:
    metadata:
      labels:
        app: postgres-database
        tier: database
    spec:
      containers:
      - name: postgres
        image: postgres:14
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-secrets
              key: password
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
        volumeMounts:
        - name: postgres-data
          mountPath: /var/lib/postgresql/data
      volumes:
      - name: postgres-data
        persistentVolumeClaim:
          claimName: postgres-data
Sample PVC with ReadWriteMany
yamlapiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-data
  namespace: production
spec:
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 10Gi
  storageClassName: nfs-standard
Command Reference
Here's a quick reference for common commands used with the StatefulSingleton operator:
Installation and Setup
bash# Install the CRDs
oc apply -f config/crd/bases/apps.openshift.yourdomain.com_statefulsingleton.yaml

# Deploy the operator
oc apply -f config/manager/manager.yaml

# Verify operator is running
oc get pods -n statefulsingleton-operator-system
Creating and Managing Resources
bash# Create a StatefulSingleton resource
oc apply -f samples/statefulsingleton.yaml

# Check status
oc get statefulsingleton

# Describe for detailed information
oc describe statefulsingleton my-database

# Delete a StatefulSingleton (will not delete the underlying deployment)
oc delete statefulsingleton my-database
Troubleshooting
bash# Check operator logs
oc logs -f deployment/statefulsingleton-controller-manager -n statefulsingleton-operator-system

# Verify webhook configuration
oc get mutatingwebhookconfigurations

# Check if pod has the readiness gate
oc get pod postgres-database-abcd1234 -o jsonpath='{.spec.readinessGates}'

# Manually check signal file existence
oc exec -it postgres-database-abcd1234 -c status-sidecar -- ls -la /var/run/signal/

# Manually create signal file (only for troubleshooting)
oc exec -it postgres-database-abcd1234 -c status-sidecar -- touch /var/run/signal/start-signal
With this implementation guide, you should now have a comprehensive understanding of how to build, deploy, and use the StatefulSingleton operator in your OpenShift environment. The operator provides a robust solution for managing stateful singleton applications without requiring application modifications, ensuring zero downtime during transitions while preventing parallel execution. │                  │
│  │  │ │ Main App   │ │        │ │ Main App   │ │ │                  │
│  │  │ │ (Running)  │ │        │ │ (Blocked)  │ │ │                  │
│  │  │ └────────────┘ │        │ └────────────┘ │ │                  │
│  │  │                │        │                │ │                  │
│  │  │ ┌────────────┐ │        │ ┌────────────┐ │
