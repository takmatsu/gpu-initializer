# GPU Initializer

The GPU Initializer is a [Kubernetes initializer](https://kubernetes.io/docs/admin/extensible-admission-controllers/#what-are-initializers) that injects the env NVIDIA_VISIBLE_DEVICES into a pod based on policy.

## Usage

```
gpu-initializer -h
```
```
Usage of gpu-initializer:
  -initializer-name string
    	The initializer name (default "gpu.initializer.kubernetes.io")
```
