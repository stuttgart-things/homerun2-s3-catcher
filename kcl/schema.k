"""
Schema definition for homerun2-s3-catcher deployment configuration
"""

schema S3Catcher:
    """Configuration for homerun2-s3-catcher Kubernetes deployment"""

    # Metadata
    name: str = "homerun2-s3-catcher"
    namespace: str = "homerun2"

    # Image
    image: str = "ghcr.io/stuttgart-things/homerun2-s3-catcher:latest"
    imagePullPolicy: str = "IfNotPresent"

    # Replicas and resources. More replicas share the consumer group; each
    # pod is its own consumer (CONSUMER_NAME defaults to the pod name).
    replicas: int = 1
    cpuRequest: str = "50m"
    cpuLimit: str = "200m"
    memoryRequest: str = "64Mi"
    memoryLimit: str = "256Mi"

    # Redis
    redisAddr: str = "redis-stack.homerun2.svc.cluster.local"
    redisPort: int | str = "6379"
    redisStream: str = "messages"
    # Comma-separated streams (REDIS_STREAMS); empty uses redisStream.
    redisStreams: str = ""
    redisPassword: str = ""

    # Consumer group. A new group starts at "0": the backlog still in the
    # stream is archived too.
    consumerGroup: str = "homerun2-s3-catcher"
    consumerStartID: str = "0"

    # S3 / S3 compatible store (MinIO, RustFS). Endpoint is host[:port].
    s3Endpoint: str = "minio.minio.svc.cluster.local:9000"
    s3Bucket: str = "homerun-archive"
    s3Region: str = "us-east-1"
    s3UseSSL: bool = False
    s3Insecure: bool = False
    s3CreateBucket: bool = False
    # Credentials: either inline (rendered into the <name>-s3 Secret) or the
    # name of an existing Secret with the keys accessKey and secretKey.
    s3AccessKey: str = ""
    s3SecretKey: str = ""
    s3ExistingSecret: str = ""

    # Archive
    keyTemplate: str = "system={{.System}}/date={{.Date}}/{{.MessageID}}.json"
    # Comma-separated systems to archive; empty archives all.
    systems: str = ""
    # Lag above which /healthz reports degraded; keep it well below
    # homerun2-scout's SCOUT_RETENTION_TTL (default 48h).
    maxLag: str = "12h"

    # UI (#7): URL prefix behind sthings-portal, e.g. "/archive"; empty
    # serves it at the root. /health and /healthz stay at the root.
    basePath: str = ""
    # The same UI a second time, opening on the Analyses tab and titled
    # "Analysis" (a second portal entry), e.g. "/analysis"; needs basePath.
    analysisBasePath: str = ""
    # Bucket of the cluster-analysis reports shown in the UI's Analyses tab;
    # empty hides them. Read with the archive's credentials.
    analysisBucket: str = ""

    # Prometheus (#9): /metrics is always served at the root. The
    # ServiceMonitor needs the prometheus-operator CRDs; serviceMonitorLabels
    # match the Prometheus' serviceMonitorSelector (e.g. release: <name>).
    serviceMonitorEnabled: bool = False
    serviceMonitorInterval: str = "60s"
    serviceMonitorLabels: {str:str} = {}

    # Health endpoint and UI
    healthPort: int | str = "8080"

    # Service
    serviceType: str = "ClusterIP"
    servicePort: int = 80
    containerPort: int = 8080

    # Extra environment variables (VISIBILITY_TIMEOUT, LOG_LEVEL, ...)
    extraEnvVars: {str:str} = {}

    # Labels and annotations
    labels: {str:str} = {}
    annotations: {str:str} = {}

    check:
        replicas >= 0, "replicas must be >= 0"
        servicePort >= 1 and servicePort <= 65535, "servicePort must be valid"
        containerPort >= 1 and containerPort <= 65535, "containerPort must be valid"
        name != "", "name must not be empty"
        namespace != "", "namespace must not be empty"
        s3Endpoint != "", "s3Endpoint must not be empty"
        s3Bucket != "", "s3Bucket must not be empty"
        not (s3ExistingSecret and (s3AccessKey or s3SecretKey)), "set either s3ExistingSecret or s3AccessKey/s3SecretKey"
