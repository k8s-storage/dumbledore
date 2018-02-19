# dumbledore: Resetting Kubernetes CSI Driver Attributes on-the-fly

## Why?

Kubernetes Storage Class only supports static parameters, dynamically determined parameters such as runtime generated encryption key,
caching, replication cannot be set on the PV.  

One example is that PVs used by database Containers must be encrypted, while PVs used by Web frontend don't need encryption.

## Target Environment

In Kubernetes 1.9+, CSI (Container Storage Interface) drivers use PV annotation to encode their runtime configurations. It is thus possible to update the attributes to provide the type of storage best for the Containerized applications.

# Mechanism

The PV annotation is modified by a dynamic webhook initializer. The initializer inspects Pod's `app` label to apply appropriate CSI attributes. If the CSI driver supports such attributes, the PVs are transformed to support these features.

# Sample Configuraton

As in [example config](examples/configmap.yaml), find the encryption settings for database and web.
In the [example pod](examples/pod.yaml), web container uses the following label to pick up PV annotation.

```yaml
  template:
    metadata:
      labels:
        app: web
```

# Acknowledgement

Some initial implementation of dynamic webhook is based on https://github.com/kelseyhightower/kubernetes-initializer-tutorial