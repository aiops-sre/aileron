/**
 * DynatraceParser - Extracts Dynatrace problem info from alert data
 * Parses problem IDs, links, cluster details, namespace info, node details, host info, etc.
 */

export interface DynatraceDetails {
  problemId: string | null;
  problemUrl: string | null;
  environment: string | null;
  impactedResource: string | null;
  resourceType: string | null;
  cluster: {
    name: string | null;
    uid: string | null;
  };
  namespace: string | null;
  workload: {
    name: string | null;
    kind: string | null;
  };
  node: {
    name: string | null;
    ip: string | null;
  };
  host: {
    name: string | null;
    ip: string | null;
  };
  rootCause: string | null;
  detectedAt: string | null;
  status: string | null;
}

export interface FormattedDetail {
  label: string;
  value: string;
  icon: string;
  isLink?: boolean;
  href?: string;
  badge?: string;
  details?: string;
  isLongText?: boolean;
}

export class DynatraceParser {
  private problemIdRegex = /P-(\d+)/g;
  private dynatraceUrlRegex = /https:\/\/[^\s]+(?:#problems\/problemdetails[^\s]*)?/g;
  private environmentRegex = /in environment\s+(\w+)/i;
  private clusterNameRegex = /k8s\.cluster\.name:\s+([^\s\n]+)/;
  private clusterIdRegex = /KUBERNETES_CLUSTER-([A-F0-9]+)/;
  private namespaceRegex = /k8s\.namespace\.name:\s+([^\s\n]+)/;
  private workloadNameRegex = /k8s\.workload\.name:\s+([^\s\n]+)/;
  private workloadKindRegex = /k8s\.workload\.kind:\s+([^\s\n]+)/;
  private nodeNameRegex = /Kubernetes node\s+([^\s\n]+)|Host\s+([^\s:]+)/;
  private hostIpRegex = /:\s+([\d.]+)\s+/;
  private impactedResourceRegex = /impacted\s+(?:application|infrastructure component)\s+(.+?)(?:\n|$)/i;

  parseAlertInfo(alertInfo: string | null | undefined): DynatraceDetails | null {
    if (!alertInfo || typeof alertInfo !== 'string') {
      return null;
    }

    const details: DynatraceDetails = {
      problemId: null,
      problemUrl: null,
      environment: null,
      impactedResource: null,
      resourceType: null,
      cluster: {
        name: null,
        uid: null
      },
      namespace: null,
      workload: {
        name: null,
        kind: null
      },
      node: {
        name: null,
        ip: null
      },
      host: {
        name: null,
        ip: null
      },
      rootCause: null,
      detectedAt: null,
      status: null
    };

    // Extract problem ID
    const problemIdMatch = alertInfo.match(/P-(\d+)/);
    if (problemIdMatch) {
      details.problemId = `P-${problemIdMatch[1]}`;
    }

    // Extract Dynatrace URL
    const urlMatch = alertInfo.match(this.dynatraceUrlRegex);
    if (urlMatch && urlMatch.length > 0) {
      details.problemUrl = urlMatch[0];
    }

    // Extract environment
    const envMatch = alertInfo.match(this.environmentRegex);
    if (envMatch) {
      details.environment = envMatch[1];
    }

    // Extract detection time
    const timeMatch = alertInfo.match(/Problem detected at:\s+([^(]+)/);
    if (timeMatch) {
      details.detectedAt = timeMatch[1].trim();
    }

    // Extract status
    const statusMatch = alertInfo.match(/^(OPEN|RESOLVED|IN_PROGRESS)/m);
    if (statusMatch) {
      details.status = statusMatch[1];
    }

    // Extract impacted resource
    const resourceMatch = alertInfo.match(this.impactedResourceRegex);
    if (resourceMatch) {
      details.impactedResource = resourceMatch[1].trim();
      // Determine resource type
      if (details.impactedResource.includes('application') || details.impactedResource.includes('workload')) {
        details.resourceType = 'workload';
      } else if (details.impactedResource.includes('node')) {
        details.resourceType = 'node';
      } else if (details.impactedResource.includes('Host')) {
        details.resourceType = 'host';
      }
    }

    // Extract cluster info
    const clusterNameMatch = alertInfo.match(this.clusterNameRegex);
    if (clusterNameMatch) {
      details.cluster.name = clusterNameMatch[1];
    }

    const clusterIdMatch = alertInfo.match(this.clusterIdRegex);
    if (clusterIdMatch) {
      details.cluster.uid = `KUBERNETES_CLUSTER-${clusterIdMatch[1]}`;
    }

    // Extract namespace
    const nsMatch = alertInfo.match(this.namespaceRegex);
    if (nsMatch) {
      details.namespace = nsMatch[1];
    }

    // Extract workload info
    const workloadNameMatch = alertInfo.match(this.workloadNameRegex);
    if (workloadNameMatch) {
      details.workload.name = workloadNameMatch[1];
    }

    const workloadKindMatch = alertInfo.match(this.workloadKindRegex);
    if (workloadKindMatch) {
      details.workload.kind = workloadKindMatch[1];
    }

    // Extract node/host name
    const nodeMatch = alertInfo.match(/(?:Kubernetes node|node)\s+([^\s\n,]+)/i);
    if (nodeMatch) {
      details.node.name = nodeMatch[1];
    }

    // Extract host info
    const hostMatch = alertInfo.match(/Host\s+([^:]+):\s+([\d.]+)/);
    if (hostMatch) {
      details.host.name = hostMatch[1];
      details.host.ip = hostMatch[2];
    }

    // Extract root cause
    const rcMatch = alertInfo.match(/Root cause\s+(.+?)(?=(?:https:|$))/s);
    if (rcMatch) {
      details.rootCause = rcMatch[1].trim().substring(0, 200); // Limit to 200 chars
    }

    return details;
  }

  formatDetails(details: DynatraceDetails | null): FormattedDetail[] | null {
    if (!details) return null;

    const formatted: FormattedDetail[] = [];

    if (details.problemId) {
      formatted.push({
        label: 'Problem ID',
        value: details.problemId,
        icon: '🔗',
        isLink: !!details.problemUrl,
        href: details.problemUrl || undefined
      });
    }

    if (details.environment) {
      formatted.push({
        label: 'Environment',
        value: details.environment,
        icon: '🌐'
      });
    }

    if (details.detectedAt) {
      formatted.push({
        label: 'Detected At',
        value: details.detectedAt,
        icon: '⏰'
      });
    }

    if (details.status) {
      formatted.push({
        label: 'Status',
        value: details.status,
        icon: '📊',
        badge: details.status
      });
    }

    if (details.impactedResource) {
      formatted.push({
        label: 'Impacted Resource',
        value: details.impactedResource,
        icon: '⚠️'
      });
    }

    // Add resource-specific details
    if (details.resourceType === 'workload') {
      if (details.cluster.name) {
        formatted.push({
          label: 'Cluster',
          value: details.cluster.name,
          icon: '🔶',
          details: details.cluster.uid || undefined
        });
      }

      if (details.namespace) {
        formatted.push({
          label: 'Namespace',
          value: details.namespace,
          icon: '📦'
        });
      }

      if (details.workload.name) {
        formatted.push({
          label: 'Workload',
          value: `${details.workload.kind || 'Unknown'}/${details.workload.name}`,
          icon: '⚙️'
        });
      }
    } else if (details.resourceType === 'node') {
      if (details.cluster.name) {
        formatted.push({
          label: 'Cluster',
          value: details.cluster.name,
          icon: '🔶'
        });
      }

      if (details.node.name) {
        formatted.push({
          label: 'Node Name',
          value: details.node.name,
          icon: '🖥️'
        });
      }
    } else if (details.resourceType === 'host') {
      if (details.host.name) {
        formatted.push({
          label: 'Host Name',
          value: details.host.name,
          icon: '🖥️'
        });
      }

      if (details.host.ip) {
        formatted.push({
          label: 'Host IP',
          value: details.host.ip,
          icon: '🌐'
        });
      }

      if (details.cluster.name) {
        formatted.push({
          label: 'Cluster',
          value: details.cluster.name,
          icon: '🔶'
        });
      }
    }

    if (details.rootCause) {
      formatted.push({
        label: 'Root Cause',
        value: details.rootCause,
        icon: '🔍',
        isLongText: true
      });
    }

    return formatted;
  }

  generateDetailsHTML(details: DynatraceDetails | null): string {
    if (!details) return '';

    const formatted = this.formatDetails(details);
    if (!formatted || formatted.length === 0) return '';

    let html = '<div class="dynatrace-details">';

    formatted.forEach(detail => {
      html += '<div class="detail-item">';
      html += `<span class="detail-icon">${detail.icon}</span>`;
      html += `<div class="detail-content">`;
      html += `<span class="detail-label">${this.escapeHtml(detail.label)}</span>`;

      if (detail.isLink && detail.href) {
        html += `<a href="${this.escapeHtml(detail.href)}" target="_blank" rel="noopener" class="detail-value detail-link">`;
        html += `${this.escapeHtml(detail.value)} <span class="external-icon">↗</span>`;
        html += `</a>`;
      } else {
        html += `<span class="detail-value${detail.isLongText ? ' long-text' : ''}">`;
        html += this.escapeHtml(detail.value);
        if (detail.details) {
          html += `<br><span class="detail-sub">${this.escapeHtml(detail.details)}</span>`;
        }
        html += `</span>`;
      }

      if (detail.badge) {
        const badgeClass = detail.badge === 'OPEN' ? 'badge-open' : 'badge-resolved';
        html += `<span class="badge ${badgeClass}">${this.escapeHtml(detail.badge)}</span>`;
      }

      html += `</div></div>`;
    });

    html += '</div>';
    return html;
  }

  escapeHtml(text: string): string {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }
}
