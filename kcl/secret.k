"""
Secrets for homerun2-s3-catcher: Redis password and S3 credentials
"""

import base64
import labels

config = labels.config

secretRedis = {
    apiVersion: "v1"
    kind: "Secret"
    metadata: {
        name: "{}-redis".format(config.name)
        namespace: config.namespace
        labels: labels.commonLabels
    }
    type: "Opaque"
    data: {
        password: base64.encode(config.redisPassword)
    }
} if config.redisPassword else None

# Rendered only for inline credentials; s3ExistingSecret points elsewhere.
secretS3 = {
    apiVersion: "v1"
    kind: "Secret"
    metadata: {
        name: "{}-s3".format(config.name)
        namespace: config.namespace
        labels: labels.commonLabels
    }
    type: "Opaque"
    data: {
        accessKey: base64.encode(config.s3AccessKey)
        secretKey: base64.encode(config.s3SecretKey)
    }
} if config.s3AccessKey and not config.s3ExistingSecret else None

# Name of the Secret the Deployment reads the S3 credentials from.
s3SecretName = config.s3ExistingSecret or "{}-s3".format(config.name)
