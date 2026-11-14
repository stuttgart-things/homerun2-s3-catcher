"""
Common labels for homerun2-s3-catcher resources
"""

import schema

_truthy = [True, "True", "true"]

# Get command-line options
_image = option("config.image")
_namespace = option("config.namespace")
_replicas = option("config.replicas")
_redisAddr = option("config.redisAddr")
_redisPort = option("config.redisPort")
_redisStream = option("config.redisStream")
_redisStreams = option("config.redisStreams")
_redisPassword = option("config.redisPassword")
_consumerGroup = option("config.consumerGroup")
_consumerStartID = option("config.consumerStartID")
_s3Endpoint = option("config.s3Endpoint")
_s3Bucket = option("config.s3Bucket")
_s3Region = option("config.s3Region")
_s3UseSSL = option("config.s3UseSSL")
_s3Insecure = option("config.s3Insecure")
_s3CreateBucket = option("config.s3CreateBucket")
_s3AccessKey = option("config.s3AccessKey")
_s3SecretKey = option("config.s3SecretKey")
_s3ExistingSecret = option("config.s3ExistingSecret")
_keyTemplate = option("config.keyTemplate")
_systems = option("config.systems")
_maxLag = option("config.maxLag")
_healthPort = option("config.healthPort")
_basePath = option("config.basePath")
_analysisBucket = option("config.analysisBucket")
_serviceMonitorEnabled = option("config.serviceMonitorEnabled")
_serviceMonitorInterval = option("config.serviceMonitorInterval")
_serviceMonitorLabels = option("config.serviceMonitorLabels") or {}
_extraEnvVars = option("config.extraEnvVars") or {}

# Config instance with command-line overrides
config: schema.S3Catcher = schema.S3Catcher {
    if _image:
        image = _image
    if _namespace:
        namespace = _namespace
    if _replicas:
        replicas = int(_replicas)
    if _redisAddr:
        redisAddr = _redisAddr
    if _redisPort:
        redisPort = _redisPort
    if _redisStream:
        redisStream = _redisStream
    if _redisStreams:
        redisStreams = _redisStreams
    if _redisPassword:
        redisPassword = _redisPassword
    if _consumerGroup:
        consumerGroup = _consumerGroup
    if _consumerStartID:
        consumerStartID = str(_consumerStartID)
    if _s3Endpoint:
        s3Endpoint = _s3Endpoint
    if _s3Bucket:
        s3Bucket = _s3Bucket
    if _s3Region:
        s3Region = _s3Region
    if _s3UseSSL != None:
        s3UseSSL = _s3UseSSL in _truthy
    if _s3Insecure != None:
        s3Insecure = _s3Insecure in _truthy
    if _s3CreateBucket != None:
        s3CreateBucket = _s3CreateBucket in _truthy
    if _s3AccessKey:
        s3AccessKey = _s3AccessKey
    if _s3SecretKey:
        s3SecretKey = _s3SecretKey
    if _s3ExistingSecret:
        s3ExistingSecret = _s3ExistingSecret
    if _keyTemplate:
        keyTemplate = _keyTemplate
    if _systems:
        systems = _systems
    if _maxLag:
        maxLag = _maxLag
    if _healthPort:
        healthPort = _healthPort
    if _basePath:
        basePath = _basePath
    if _analysisBucket:
        analysisBucket = _analysisBucket
    if _serviceMonitorEnabled != None:
        serviceMonitorEnabled = _serviceMonitorEnabled in _truthy
    if _serviceMonitorInterval:
        serviceMonitorInterval = _serviceMonitorInterval
    if _serviceMonitorLabels:
        serviceMonitorLabels = _serviceMonitorLabels
    if _extraEnvVars:
        extraEnvVars = _extraEnvVars
}

# Common labels applied to all resources
commonLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
    "app.kubernetes.io/component": "s3-catcher"
    "app.kubernetes.io/part-of": "homerun2"
    **config.labels
}

# Selector labels for pods
selectorLabels = {
    "app.kubernetes.io/name": config.name
    "app.kubernetes.io/instance": config.name
}
