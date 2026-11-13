"""
Service for homerun2-s3-catcher
"""

import labels

config = labels.config

service = {
    apiVersion: "v1"
    kind: "Service"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: labels.commonLabels
    }
    spec: {
        type: config.serviceType
        ports: [
            {
                name: "http"
                port: config.servicePort
                targetPort: "http"
                protocol: "TCP"
            }
        ]
        selector: labels.selectorLabels
    }
}
