"""
ConfigMap for homerun2-s3-catcher: the non-secret settings
"""

import labels

config = labels.config

configMap = {
    apiVersion: "v1"
    kind: "ConfigMap"
    metadata: {
        name: "{}-config".format(config.name)
        namespace: config.namespace
        labels: labels.commonLabels
    }
    data: {
        LOG_LEVEL: "info"
        REDIS_ADDR: config.redisAddr
        REDIS_PORT: str(config.redisPort)
        REDIS_STREAM: config.redisStream
        if config.redisStreams:
            REDIS_STREAMS: config.redisStreams
        CONSUMER_GROUP: config.consumerGroup
        CONSUMER_START_ID: config.consumerStartID
        S3_ENDPOINT: config.s3Endpoint
        S3_BUCKET: config.s3Bucket
        S3_REGION: config.s3Region
        S3_USE_SSL: "true" if config.s3UseSSL else "false"
        S3_INSECURE: "true" if config.s3Insecure else "false"
        S3_CREATE_BUCKET: "true" if config.s3CreateBucket else "false"
        S3_KEY_TEMPLATE: config.keyTemplate
        if config.systems:
            SYSTEMS: config.systems
        MAX_LAG: config.maxLag
        HEALTH_PORT: str(config.healthPort)
        **config.extraEnvVars
    }
}
