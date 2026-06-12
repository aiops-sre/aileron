// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// PagerDuty On-Call API Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

// User
export interface PDUser {
  id: string;
  name: string;
  email: string;
  avatar_url?: string;
  role?: string;
}

// Schedule
export interface PDSchedule {
  id: string;
  name: string;
  description?: string;
  time_zone: string;
}

// Shift
export interface PDShift {
  user: PDUser;
  start: string;
  end: string;
}

// Current On-Call
export interface PDCurrentOnCall {
  schedule_id: string;
  schedule_name: string;
  user: PDUser;
  start: string;
  end: string;
}

// Upcoming On-Call
export interface PDUpcomingOnCall {
  schedule_id: string;
  schedule_name: string;
  shifts: PDShift[];
}

// API Responses
export interface PDCurrentOnCallResponse {
  data: PDCurrentOnCall[];
  cached?: boolean;
  cache_timestamp?: string;
}

export interface PDUpcomingOnCallResponse {
  data: PDUpcomingOnCall[];
  cached?: boolean;
  cache_timestamp?: string;
}

export interface PDSchedulesResponse {
  data: PDSchedule[];
}

export interface PDUsersResponse {
  data: PDUser[];
}

// Incident Types
export interface PDService {
  id: string;
  name: string;
}

export interface PDAssignee {
  id: string;
  name: string;
  email: string;
}

export interface PDIncident {
  id: string;
  incident_number: number;
  title: string;
  status: 'triggered' | 'acknowledged' | 'resolved';
  urgency: 'high' | 'low';
  created_at: string;
  acknowledged_at?: string;
  resolved_at?: string;
  service: PDService;
  assignees: PDAssignee[];
}

export interface PDPagination {
  limit: number;
  offset: number;
  total: number;
  more: boolean;
}

export interface PDIncidentsResponse {
  data: PDIncident[];
  pagination: PDPagination;
  cached?: boolean;
  cache_timestamp?: string;
}

export interface PDIncidentStats {
  total_incidents: number;
  by_status: {
    triggered: number;
    acknowledged: number;
    resolved: number;
  };
  by_urgency: {
    high: number;
    low: number;
  };
  avg_resolution_time_seconds: number;
  avg_acknowledgment_time_seconds: number;
}

export interface PDIncidentStatsResponse {
  data: PDIncidentStats;
  cached?: boolean;
  cache_timestamp?: string;
}

// System Status Types
export interface PDComponentStatus {
  status: 'operational' | 'degraded' | 'down';
  latency_ms: number;
}

export interface PDSystemStatus {
  status: 'operational' | 'degraded' | 'down';
  components: {
    cache: PDComponentStatus;
    database: PDComponentStatus;
    pagerduty_api: PDComponentStatus;
  };
  uptime_seconds: number;
  timestamp: string;
}

// Health Check Types
export interface PDHealthResponse {
  status: 'healthy' | 'unhealthy';
  cache: 'connected' | 'disconnected';
  database: 'connected' | 'disconnected';
  timestamp: string;
}
