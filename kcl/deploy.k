"""
Deployment for homerun2-s3-catcher
"""

import labels
import secret

config = labels.config

deployment = {
    apiVersion: "apps/v1"
    kind: "Deployment"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: labels.commonLabels
    }
    spec: {
        replicas: config.replicas
        selector: {
            matchLabels: labels.selectorLabels
        }
        strategy: {
            type: "RollingUpdate"
            rollingUpdate: {
                maxSurge: 1
                maxUnavailable: 0
            }
        }
        template: {
            metadata: {
                labels: labels.commonLabels
            }
            spec: {
                serviceAccountName: config.name
                containers: [
                    {
                        name: config.name
                        image: config.image
                        imagePullPolicy: config.imagePullPolicy
                        ports: [
                            {
                                name: "http"
                                containerPort: config.containerPort
                                protocol: "TCP"
                            }
                        ]
                        # Non-secret settings come from the ConfigMap (envFrom).
                        env: [
                            if config.redisPassword:
                                {
                                    name: "REDIS_PASSWORD"
                                    valueFrom: {
                                        secretKeyRef: {
                                            name: "{}-redis".format(config.name)
                                            key: "password"
                                        }
                                    }
                                }
                            {
                                name: "S3_ACCESS_KEY"
                                valueFrom: {
                                    secretKeyRef: {
                                        name: secret.s3SecretName
                                        key: "accessKey"
                                    }
                                }
                            }
                            {
                                name: "S3_SECRET_KEY"
                                valueFrom: {
                                    secretKeyRef: {
                                        name: secret.s3SecretName
                                        key: "secretKey"
                                    }
                                }
                            }
                            # Each pod its own consumer in the group.
                            {
                                name: "CONSUMER_NAME"
                                valueFrom: {
                                    fieldRef: {
                                        fieldPath: "metadata.name"
                                    }
                                }
                            }
                        ]
                        envFrom: [
                            {
                                configMapRef: {
                                    name: "{}-config".format(config.name)
                                }
                            }
                        ]
                        resources: {
                            requests: {
                                cpu: config.cpuRequest
                                memory: config.memoryRequest
                            }
                            limits: {
                                cpu: config.cpuLimit
                                memory: config.memoryLimit
                            }
                        }
                        # Startup waits up to REDIS_STARTUP_TIMEOUT (default 120s)
                        # for Redis and up to 2m for the bucket before the
                        # health server starts. 60 x 5s = 300s covers both.
                        # A lag above MAX_LAG is "degraded" with HTTP 200, so
                        # the probes do not restart a catcher that is behind.
                        startupProbe: {
                            httpGet: {
                                path: "/healthz"
                                port: "http"
                            }
                            periodSeconds: 5
                            timeoutSeconds: 3
                            failureThreshold: 60
                        }
                        livenessProbe: {
                            httpGet: {
                                path: "/healthz"
                                port: "http"
                            }
                            initialDelaySeconds: 10
                            periodSeconds: 10
                            timeoutSeconds: 5
                            failureThreshold: 3
                        }
                        readinessProbe: {
                            httpGet: {
                                path: "/healthz"
                                port: "http"
                            }
                            initialDelaySeconds: 5
                            periodSeconds: 5
                            timeoutSeconds: 3
                            failureThreshold: 3
                        }
                        securityContext: {
                            readOnlyRootFilesystem: True
                            runAsNonRoot: True
                            allowPrivilegeEscalation: False
                            capabilities: {
                                drop: ["ALL"]
                            }
                        }
                        volumeMounts: [
                            {
                                name: "tmp"
                                mountPath: "/tmp"
                            }
                        ]
                    }
                ]
                securityContext: {
                    runAsNonRoot: True
                    runAsUser: 65532
                    runAsGroup: 65532
                    fsGroup: 65532
                    seccompProfile: {
                        type: "RuntimeDefault"
                    }
                }
                volumes: [
                    {
                        name: "tmp"
                        emptyDir: {}
                    }
                ]
                affinity: {
                    podAntiAffinity: {
                        preferredDuringSchedulingIgnoredDuringExecution: [
                            {
                                weight: 100
                                podAffinityTerm: {
                                    labelSelector: {
                                        matchLabels: labels.selectorLabels
                                    }
                                    topologyKey: "kubernetes.io/hostname"
                                }
                            }
                        ]
                    }
                }
            }
        }
    }
}
