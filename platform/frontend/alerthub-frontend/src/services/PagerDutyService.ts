// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// PagerDuty On-Call API Service
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import type {
  PDCurrentOnCallResponse,
  PDUpcomingOnCallResponse,
  PDSchedulesResponse,
  PDUsersResponse,
  PDIncidentsResponse,
  PDIncidentStatsResponse,
  PDSystemStatus,
  PDHealthResponse,
} from '@/types/pagerduty'

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Configuration
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

const PD_API_CONFIG = {
  // Use relative URL for all environments — nginx proxies to oncall-pd in production
  BASE_URL: '/pagerduty',
  TIMEOUT: 30000, // 30 seconds
} as const

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// API Client
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

class PagerDutyAPIClient {
  private baseUrl: string

  constructor() {
    this.baseUrl = PD_API_CONFIG.BASE_URL
  }

  /**
   * Make API request with proper headers and error handling
   */
  private async makeRequest<T>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<T> {
    const url = `${this.baseUrl}/${endpoint}`

    // Attach IDMS access token so nginx can forward it to oncall-pd
    const idmsToken = localStorage.getItem('oauth_id_token') || localStorage.getItem('floodgate_token') || ''

    const requestHeaders: Record<string, string> = {
      'Accept': 'application/json',
      'Content-Type': 'application/json',
    }
    if (idmsToken) {
      requestHeaders['Authorization'] = `Bearer ${idmsToken}`
    }

    const defaultOptions: RequestInit = {
      credentials: 'include',
      headers: {
        ...requestHeaders,
        ...(options.headers as Record<string, string> | undefined),
      },
      ...options,
    }

    try {

      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), PD_API_CONFIG.TIMEOUT)

      const response = await fetch(url, {
        ...defaultOptions,
        signal: controller.signal,
      })

      clearTimeout(timeoutId)

      if (!response.ok) {
        const errorText = await response.text()
        throw new Error(
          `API request failed: ${response.status} ${response.statusText} - ${errorText}`
        )
      }

      const data = await response.json()
      return data
    } catch (error) {
      if (error instanceof Error) {
        if (error.name === 'AbortError') {
          throw new Error('Request timeout')
        }
        console.error('[PagerDuty API] Request failed:', error.message.substring(0, 200))
      }
      throw error
    }
  }

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // Health Check
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * Check API health
   */
  async checkHealth(): Promise<PDHealthResponse> {
    return this.makeRequest<PDHealthResponse>('health')
  }

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // On-Call Endpoints
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * Get current on-call personnel
   */
  async getCurrentOnCall(scheduleIds?: string[]): Promise<PDCurrentOnCallResponse> {
    const params = new URLSearchParams()
    if (scheduleIds && scheduleIds.length > 0) {
      params.append('schedule_ids', scheduleIds.join(','))
    }

    const endpoint = params.toString()
      ? `oncall/current?${params.toString()}`
      : 'oncall/current'

    return this.makeRequest<PDCurrentOnCallResponse>(endpoint)
  }

  /**
   * Get upcoming on-call shifts
   */
  async getUpcomingOnCall(
    days: number = 7,
    scheduleIds?: string[]
  ): Promise<PDUpcomingOnCallResponse> {
    const params = new URLSearchParams()
    params.append('days', days.toString())
    
    if (scheduleIds && scheduleIds.length > 0) {
      params.append('schedule_ids', scheduleIds.join(','))
    }

    return this.makeRequest<PDUpcomingOnCallResponse>(
      `oncall/upcoming?${params.toString()}`
    )
  }

  /**
   * Get all schedules
   */
  async getSchedules(): Promise<PDSchedulesResponse> {
    return this.makeRequest<PDSchedulesResponse>('oncall/schedules')
  }

  /**
   * Get all users
   */
  async getUsers(): Promise<PDUsersResponse> {
    return this.makeRequest<PDUsersResponse>('oncall/users')
  }

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // Incidents Endpoints
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * Get incidents with optional filters
   */
  async getIncidents(params?: {
    status?: 'triggered' | 'acknowledged' | 'resolved';
    service_ids?: string[];
    team_ids?: string[];
    since?: string;
    until?: string;
    limit?: number;
    offset?: number;
  }): Promise<PDIncidentsResponse> {
    const searchParams = new URLSearchParams()

    if (params) {
      if (params.status) searchParams.append('status', params.status)
      if (params.service_ids) searchParams.append('service_ids', params.service_ids.join(','))
      if (params.team_ids) searchParams.append('team_ids', params.team_ids.join(','))
      if (params.since) searchParams.append('since', params.since)
      if (params.until) searchParams.append('until', params.until)
      if (params.limit) searchParams.append('limit', params.limit.toString())
      if (params.offset) searchParams.append('offset', params.offset.toString())
    }

    const endpoint = searchParams.toString()
      ? `incidents?${searchParams.toString()}`
      : 'incidents'

    return this.makeRequest<PDIncidentsResponse>(endpoint)
  }

  /**
   * Get incident statistics
   */
  async getIncidentStats(params?: {
    service_ids?: string[];
    team_ids?: string[];
    since?: string;
    until?: string;
  }): Promise<PDIncidentStatsResponse> {
    const searchParams = new URLSearchParams()

    if (params) {
      if (params.service_ids) searchParams.append('service_ids', params.service_ids.join(','))
      if (params.team_ids) searchParams.append('team_ids', params.team_ids.join(','))
      if (params.since) searchParams.append('since', params.since)
      if (params.until) searchParams.append('until', params.until)
    }

    const endpoint = searchParams.toString()
      ? `incidents/stats?${searchParams.toString()}`
      : 'incidents/stats'

    return this.makeRequest<PDIncidentStatsResponse>(endpoint)
  }

  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  // System Status
  // ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  /**
   * Get system status
   */
  async getSystemStatus(): Promise<PDSystemStatus> {
    return this.makeRequest<PDSystemStatus>('system/status')
  }
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Export Singleton Instance
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

export const pagerDutyService = new PagerDutyAPIClient()
export default pagerDutyService
