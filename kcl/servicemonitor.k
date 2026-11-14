"""
ServiceMonitor for homerun2-s3-catcher (prometheus-operator), optional:
serviceMonitorEnabled. Prometheus scrapes /metrics at the root, whatever the
BASE_PATH.
"""

import labels

config = labels.config

serviceMonitor = {
    apiVersion: "monitoring.coreos.com/v1"
    kind: "ServiceMonitor"
    metadata: {
        name: config.name
        namespace: config.namespace
        labels: {
            **labels.commonLabels
            **config.serviceMonitorLabels
        }
    }
    spec: {
        selector.matchLabels: labels.selectorLabels
        namespaceSelector.matchNames: [config.namespace]
        endpoints: [
            {
                port: "http"
                path: "/metrics"
                interval: config.serviceMonitorInterval
            }
        ]
    }
} if config.serviceMonitorEnabled else None
