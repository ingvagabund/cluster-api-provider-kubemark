# Kubemark actuator proposal

## Motivation

Actuators are key stones of cluster API infrastructure. They provide an abstract way
of communicating with cloud providers. Requesting for resources (e.g. instances) specified
by machine objects. Actuator (socketed into a machine controller) allows to build
a generic concept of a machine that is then leveraged by other components
of the cluster API stack such as machineset or machinedeployment. Machineset,
resp. machinedeployment allow to define a concept of a machine pool which opens
an opportunity for generalizing notion of cluster auto-scaling and machine self-healing.

One of machine use cases is to represent a cluster node which registers itself into
a cluster. It may take minutes before a node joins a cluster. Which can be time consuming
during development and testing phase. Or, to cover various failure and recovery
scenarios it may be even impossible to utilize real nodes to get a cluster into a failure
state that triggers suitable recovery procedure. Thus, we need more control about
how individual nodes behave.

## Kubemark

Kubernetes already provides solution called Kubemark ([1]) that allows to create
fake nodes that can register into a cluster. It's basically running kubelet with
fake container runtime. With minor modification we can deploy such a node
and configure it to behave to our likings. E.g. set when node starts reporting
NotReady status.

[1] TODO(jchaloup): add link to kubemark

### Integration with cluster-api

Given each kubemark node (also called hollow node) is run as a pod,
every machine can be corresponded with exactly one kubemark node.
Which allows straightforward implementation of actuator that translates
create, update, exists and delete operation into corresponding pod operations.
This new kubemark actuator can be then deployed as any other actuator.
Providing better control over cluster nodes, decreasing development and testing,
and saving resources that would be spend on actually provisioning real nodes.

### Provider configuration

By default, every kubemark node joins a cluster and starts reporting Ready status.


## TODO

- kubemark provider config v1alpha1
- runtime disruptor
- cluster autoscaler (how to use it in testing the scaling loop)
-
