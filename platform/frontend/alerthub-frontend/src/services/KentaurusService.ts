// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple HCL Kentaurus API Service (via Backend Proxy)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import type {
  KentaurusCreateIncidentRequest,
  KentaurusCreateIncidentResponse,
  KentaurusQueryRequest,
  KentaurusQueryResponse,
  KentaurusIncidentRecord,
  KentaurusIncident,
  DateRange,
} from '@/types/kentaurus'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Configuration - Use backend proxy (no direct IDMS/Kentaurus calls from browser)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const KENTAURUS_CONFIG = {
  // Backend handles IDMS auth and Kentaurus API calls
  API_BASE_URL: '/api/v1/incidents/hcl',
  ASSIGNMENT_GROUP_ID: '740df08c2d0dea104c5df117ebee2a1f',
  BUSINESS_ORG_ID: '7fad712f55b0b200b7b1b313be98e279',
  CONFIGURATION: 'Interactive-Infra-ADC',
  APP_ID: '928952',
} as const

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// API Client
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

class KentaurusAPIClient {
  private maxRetries = 3
  private retryDelay = 1000

  /**
   * Make authenticated API request via backend
   */
  private async makeRequest<T>(
    endpoint: string,
    options: RequestInit = {},
    retryCount = 0
  ): Promise<T> {
    try {
      const url = `${KENTAURUS_CONFIG.API_BASE_URL}${endpoint}`

      // Get auth token — check sessionStorage first, fall back to localStorage
      const token = sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''

      const response = await fetch(url, {
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          ...(token && { 'Authorization': `Bearer ${token}` }),
          ...options.headers,
        },
        ...options,
      })

      if (!response.ok) {
        const errorText = await response.text()
        throw new Error(`API request failed: ${response.status} ${response.statusText} - ${errorText}`)
      }

      return await response.json()
    } catch (error) {
      // Retry on network errors
      if (retryCount < this.maxRetries && this.isRetryableError(error)) {
        const delay = this.retryDelay * Math.pow(2, retryCount)
        await this.sleep(delay)
        return this.makeRequest<T>(endpoint, options, retryCount + 1)
      }

      console.error('[Kentaurus API] Request failed:', error instanceof Error ? error.message.substring(0, 200) : 'unknown')
      throw error
    }
  }

  private isRetryableError(error: any): boolean {
    return (
      error instanceof TypeError ||
      error.message?.includes('fetch') ||
      error.message?.includes('network')
    )
  }

  private sleep(ms: number): Promise<void> {
    return new Promise(resolve => setTimeout(resolve, ms))
  }

  /**
   * Create a new incident via backend
   */
  async createIncident(
    request: Omit<KentaurusCreateIncidentRequest, 'module' | 'callingApp' | 'configuration'>
  ): Promise<KentaurusCreateIncidentResponse> {

    const fullRequest = {
      module: 'incident',
      callingApp: KENTAURUS_CONFIG.APP_ID,
      configuration: KENTAURUS_CONFIG.CONFIGURATION,
      assignmentGroupId: KENTAURUS_CONFIG.ASSIGNMENT_GROUP_ID,
      ...request,
    }

    try {
      const response = await this.makeRequest<any>('/create', {
        method: 'POST',
        body: JSON.stringify(fullRequest),
      })

      return {
        success: true,
        data: response.data || response,
      }
    } catch (error) {
      console.error('[Kentaurus API] Failed to create incident:', error instanceof Error ? error.message.substring(0, 200) : 'unknown')
      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      }
    }
  }

  /**
   * Update an incident via backend (acknowledge, add work notes, change status)
   */
  async updateIncident(
    ticketId: string,
    fields: {
      ticketStatus?: string
      workLog?: string
      additionalComments?: string
      resolution?: string
      resolutionSummary?: string
      impact?: '1' | '2' | '3'
      urgency?: '1' | '2' | '3'
    }
  ): Promise<{ success: boolean; error?: string }> {
    try {
      await this.makeRequest<any>(`/${ticketId}`, {
        method: 'PUT',
        body: JSON.stringify({
          module: 'incident',
          ...fields,
        }),
      })
      return { success: true }
    } catch (error) {
      console.error('[Kentaurus API] Update failed:', error instanceof Error ? error.message.substring(0, 200) : 'unknown')
      return { success: false, error: error instanceof Error ? error.message : 'Unknown error' }
    }
  }

  /**
   * Reopen a resolved or closed incident via backend
   */
  async reopenIncident(
    number: string,
    comment: string
  ): Promise<{ success: boolean; error?: string }> {
    try {
      await this.makeRequest<any>(`/${number}/reopen`, {
        method: 'POST',
        body: JSON.stringify({ comment }),
      })
      return { success: true }
    } catch (error) {
      console.error('[Kentaurus API] Reopen failed:', error instanceof Error ? error.message.substring(0, 200) : 'unknown')
      return { success: false, error: error instanceof Error ? error.message : 'Unknown error' }
    }
  }

  /**
   * Query incidents via backend — single page, respects Kentaurus 100/page limit
   */
  async queryIncidents(
    dateRange: DateRange,
    filters: any = {},
    offset = 0,
    count = 100
  ): Promise<KentaurusQueryResponse> {
    const safeCount = Math.min(count, 100)

    const queryRequest = {
      module: 'incident',
      callingApp: KENTAURUS_CONFIG.APP_ID,
      filters: {
        assignment_group: KENTAURUS_CONFIG.ASSIGNMENT_GROUP_ID,
        u_business_org: KENTAURUS_CONFIG.BUSINESS_ORG_ID,
        ...filters,
      },
      pagination: {
        offset,
        count: safeCount,
      },
      dateRange: {
        start: dateRange.start,
        end: dateRange.end,
      },
      sortOrder: 'desc',
    }

    try {
      const response = await this.makeRequest<any>('/query', {
        method: 'POST',
        body: JSON.stringify(queryRequest),
      })

      const records = response.result?.data || response.data?.records || []
      const metaTotal = response.result?.meta?.queryResultCount || response.result?.meta?.resultCount || 0
      // When Kentaurus omits total count but returned a full page, assume at least one more page exists.
      // The next loadPage call will correct the total once we know how many records that page has.
      const total = metaTotal > 0
        ? metaTotal
        : records.length >= safeCount
          ? offset + records.length + safeCount
          : offset + records.length

      return {
        success: true,
        data: { records, total, offset, count: safeCount },
      }
    } catch (error) {
      console.error('[Kentaurus API] Query failed:', error instanceof Error ? error.message.substring(0, 200) : 'unknown')
      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      }
    }
  }


  async queryCurrentMonthIncidents(): Promise<KentaurusQueryResponse> {
    const now = new Date()
    const start = new Date(now.getFullYear(), now.getMonth(), 1)
    const end = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 23, 59, 59)

    return this.queryIncidents({
      start: start.toISOString(),
      end: end.toISOString(),
      type: 'current_month',
    })
  }

  /**
   * Query incidents for a specific month
   */
  async queryMonthIncidents(year: number, month: number): Promise<KentaurusQueryResponse> {
    const start = new Date(year, month, 1)
    const end = new Date(year, month + 1, 0, 23, 59, 59)

    return this.queryIncidents({
      start: start.toISOString(),
      end: end.toISOString(),
      type: 'custom',
    })
  }
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Helper Functions
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

/**
 * Convert Kentaurus incident record to UI incident
 */
export function mapKentaurusIncident(record: KentaurusIncidentRecord): KentaurusIncident {
  // impact/urgency/priority arrive as "1 - High", "2 - Medium", "3 - Low"
  const impactNum = (record.impact?.split(' ')[0] || '3') as '1' | '2' | '3'
  const urgencyNum = (record.urgency?.split(' ')[0] || '3') as '1' | '2' | '3'

  const severityMap: Record<string, 'critical' | 'high' | 'medium' | 'low'> = {
    '1': 'critical',
    '2': 'high',
    '3': 'low',
  }

  // state arrives as "Open", "In Progress", "Resolved", "Closed"
  const stateNorm = (record.state || '').toLowerCase()
  let status: 'open' | 'investigating' | 'resolved'
  if (stateNorm === 'closed' || stateNorm === 'resolved') status = 'resolved'
  else if (stateNorm.includes('progress') || stateNorm.includes('work')) status = 'investigating'
  else status = 'open'

  const openedAt = record.openedDate || record.lastModifiedDate || new Date().toISOString()
  const resolvedAt = record.resolvedDate || undefined
  const closedAt = record.closedDate || undefined

  const timeToResolve =
    resolvedAt
      ? Math.floor((new Date(resolvedAt).getTime() - new Date(openedAt).getTime()) / 60000)
      : undefined

  return {
    id: record.UUID,
    sys_id: record.UUID,
    incident_number: record.number,
    title: record.title || 'Untitled Incident',
    description: record.description || '',
    severity: severityMap[impactNum] || 'low',
    status,
    created_at: openedAt,
    updated_at: record.lastModifiedDate || openedAt,
    started_at: openedAt,
    opened_at: openedAt,
    resolved_at: resolvedAt,
    closed_at: closedAt,
    assigned_to: record.assignedTo?.email || '',
    assigned_to_name: record.assignedTo?.name || '',
    affected_services: record.configurationItem?.name || record.environment || '',
    related_alerts: [],
    impact_level: impactNum,
    urgency_level: urgencyNum,
    priority: record.priority || '',
    state: record.state || '',
    assignment_group: record.assignmentGroup?.UUID || '',
    assignment_group_name: record.assignmentGroup?.name || '',
    environment: record.environment,
    category: record.category,
    subcategory: record.subCategory,
    close_notes: record.resolutionSummary,
    time_to_resolve: timeToResolve,
    sla_breach: false,
  }
}

/**
 * Calculate incident metrics from list
 */
export function calculateIncidentMetrics(incidents: KentaurusIncident[]) {
  const metrics = {
    total: incidents.length,
    open: 0,
    investigating: 0,
    resolved: 0,
    closed: 0,
    avgResolutionTime: 0,
    avgAcknowledgmentTime: 0,
    slaBreach: 0,
    byImpact: { high: 0, medium: 0, low: 0 },
    byUrgency: { high: 0, medium: 0, low: 0 },
    byAssignee: {} as Record<string, number>,
    byMonth: [] as Array<{ month: string; total: number; resolved: number; open: number }>,
    trend: [] as Array<{ date: string; created: number; resolved: number }>,
  }

  let totalResolutionTime = 0
  let resolvedCount = 0

  incidents.forEach(incident => {
    if (incident.status === 'open') metrics.open++
    else if (incident.status === 'investigating') metrics.investigating++
    else if (incident.status === 'resolved') {
      metrics.resolved++
      if (incident.time_to_resolve) {
        totalResolutionTime += incident.time_to_resolve
        resolvedCount++
      }
    }

    if (incident.closed_at) metrics.closed++

    if (incident.impact_level === '1') metrics.byImpact.high++
    else if (incident.impact_level === '2') metrics.byImpact.medium++
    else metrics.byImpact.low++

    if (incident.urgency_level === '1') metrics.byUrgency.high++
    else if (incident.urgency_level === '2') metrics.byUrgency.medium++
    else metrics.byUrgency.low++

    if (incident.assigned_to_name) {
      metrics.byAssignee[incident.assigned_to_name] =
        (metrics.byAssignee[incident.assigned_to_name] || 0) + 1
    }

    if (incident.sla_breach) metrics.slaBreach++
  })

  metrics.avgResolutionTime = resolvedCount > 0 ? totalResolutionTime / resolvedCount / 60 : 0

  return metrics
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Export Singleton Instance
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const kentaurusService = new KentaurusAPIClient()
export default kentaurusService
