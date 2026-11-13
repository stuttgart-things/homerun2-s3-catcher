"""
Main entry point for homerun2-s3-catcher Kubernetes deployment
Imports all resources and exports manifests
"""

import serviceaccount
import configmap
import secret
import deploy
import service

# Export all manifests (filter out None values).
# Namespace is NOT emitted from the KCL profile — the consuming Application
# uses `CreateNamespace=true`, and PR-preview namespaces are pre-created by
# Kyverno. See stuttgart-things/homerun2-omni-pitcher#132.
manifests = [
    m for m in [
        serviceaccount.serviceAccount
        configmap.configMap
        secret.secretRedis
        secret.secretS3
        deploy.deployment
        service.service
    ] if m
]
