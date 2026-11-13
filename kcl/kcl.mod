[package]
name = "deploy-homerun2-s3-catcher"
version = "0.1.0"
description = "KCL module for deploying homerun2-s3-catcher on Kubernetes"

[dependencies]
k8s = "1.36"

[profile]
entries = [
    "main.k"
]
