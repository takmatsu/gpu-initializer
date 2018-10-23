# Kubernetes GPU Initializer

This injects the env NVIDIA_VISIBLE_DEVICES into uninitialized Pods. 

## Prerequisites

Kubernetes 1.7.0+ is required with [support for Initializers enabled](https://kubernetes.io/docs/admin/extensible-admission-controllers/#enable-initializers-alpha-feature). If you're using Google Container Engine create an alpha cluster:

```
gcloud alpha container clusters create k0 \
  --enable-kubernetes-alpha \
  --cluster-version 1.7.0
```
