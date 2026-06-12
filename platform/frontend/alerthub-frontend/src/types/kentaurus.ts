// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// Apple HCL Kentaurus API Types
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import type { Incident, IncidentFilters } from './index'

// Authentication
export interface KentaurusAuthRequest {
  appId: string;
  appPassword: string;
  otherApp: string;
  context: string;
  contextVersion: number;
  timeToLive: number;
}

export interface KentaurusAuthResponse {
  success: boolean;
  token?: string;
  expiresAt?: number;
  error?: string;
}

export interface KentaurusAuthToken {
  token: string;
  expiresAt: number;
  createdAt: number;
}

// Incident Creation
export interface KentaurusCreateIncidentRequest {
  module: 'incident';
  callingApp: string;
  configuration: string;
  title: string;
  description: string;
  impact: '1' | '2' | '3'; // 1-High, 2-Medium, 3-Low
  urgency: '1' | '2' | '3'; // 1-High, 2-Medium, 3-Low
  assignmentGroupId?: string;
  environment?: string;
  affectedServices?: string;
  contactType?: string;
  category?: string;
  subcategory?: string;
  assignedTo?: string;
}

export interface KentaurusCreateIncidentResponse {
  success: boolean;
  data?: {
    incident_id: string;
    incident_number: string;
    sys_id: string;
  };
  error?: string;
  message?: string;
}

// Incident Query
export interface KentaurusQueryRequest {
  module: 'incident';
  callingApp: string;
  filters: {
    assignment_group?: string;
    u_business_org?: string;
    opened_at?: string;
    state?: string;
    impact?: string;
    urgency?: string;
    assigned_to?: string;
  };
  pagination: {
    offset: number;
    count: number;
  };
  dateRange?: {
    start: string;
    end: string;
  };
}

// Matches the actual Kentaurus API response shape (camelCase)
export interface KentaurusIncidentRecord {
  UUID: string;
  number: string;
  title: string;
  description: string;
  impact: string;      // e.g. "1 - High", "2 - Medium", "3 - Low"
  urgency: string;     // e.g. "3 - Low"
  priority: string;    // e.g. "2 - High"
  state: string;       // e.g. "Open", "In Progress", "Resolved", "Closed"
  category?: string;
  subCategory?: string;
  environment?: string;
  resolution?: string;
  resolutionSummary?: string;
  openedDate: string;
  resolvedDate?: string;
  closedDate?: string;
  lastModifiedDate?: string;
  assignmentGroup: {
    name: string;
    UUID: string;
    dsid: string;
    email?: string;
  };
  assignedTo: {
    name: string;
    email: string;
    dsid: string;
    vip?: boolean;
  };
  openedBy?: { name: string; email: string };
  closedBy?: { name: string; email: string };
  resolvedBy?: { name: string; email: string };
  callerID?: { name: string; email: string; dsid: string };
  configurationItem?: { name: string; UUID: string };
  businessOrganization?: string;
  active?: boolean;
  reassignmentCount?: number;
  reopenCount?: number;
}

export interface KentaurusQueryResponse {
  success: boolean;
  data?: {
    records: KentaurusIncidentRecord[];
    total: number;
    offset: number;
    count: number;
  };
  error?: string;
  message?: string;
}

// Enhanced Incident Types for UI
export interface KentaurusIncident extends Incident {
  sys_id: string;
  impact_level: '1' | '2' | '3';
  urgency_level: '1' | '2' | '3';
  priority: string;
  state: string;
  opened_at: string;
  resolved_at?: string;
  closed_at?: string;
  assignment_group: string;
  assignment_group_name: string;
  u_business_org?: string;
  environment?: string;
  category?: string;
  subcategory?: string;
  close_notes?: string;
  work_notes?: string;
  comments?: string;
  time_to_resolve?: number; // in minutes
  time_to_acknowledge?: number; // in minutes
  sla_breach?: boolean;
}

// Metrics and KPIs
export interface IncidentMetrics {
  total: number;
  open: number;
  investigating: number;
  resolved: number;
  closed: number;
  avgResolutionTime: number; // in hours
  avgAcknowledgmentTime: number; // in minutes
  slaBreach: number;
  byImpact: {
    high: number;
    medium: number;
    low: number;
  };
  byUrgency: {
    high: number;
    medium: number;
    low: number;
  };
  byAssignee: Record<string, number>;
  byMonth: Array<{
    month: string;
    total: number;
    resolved: number;
    open: number;
  }>;
  trend: Array<{
    date: string;
    created: number;
    resolved: number;
  }>;
}

// Date Range Filters
export type DateRangeType = 'current_month' | 'last_month' | 'last_3_months' | 'last_6_months' | 'custom';

export interface DateRange {
  start: string;
  end: string;
  type: DateRangeType;
}

// Enhanced Incident Filters
export interface KentaurusIncidentFilters extends IncidentFilters {
  impact?: '1' | '2' | '3' | '';
  urgency?: '1' | '2' | '3' | '';
  state?: string;
  assignment_group?: string;
  u_business_org?: string;
  environment?: string;
  date_range?: DateRange;
  sla_breach?: boolean;
}

// Export Options
export interface ExportOptions {
  format: 'csv' | 'json' | 'excel' | 'pdf';
  fields: string[];
  filters: KentaurusIncidentFilters;
  dateRange: DateRange;
}

// Bulk Operations
export interface BulkOperation {
  action: 'assign' | 'resolve' | 'close' | 'update_priority' | 'add_note';
  incident_ids: string[];
  data: Record<string, any>;
}

export interface BulkOperationResult {
  success: boolean;
  processed: number;
  failed: number;
  errors: Array<{
    incident_id: string;
    error: string;
  }>;
}

// Notifications
export interface IncidentNotification {
  id: string;
  incident_id: string;
  incident_number: string;
  type: 'created' | 'updated' | 'assigned' | 'resolved' | 'sla_breach';
  title: string;
  message: string;
  severity: 'critical' | 'high' | 'medium' | 'low';
  timestamp: string;
  read: boolean;
}

// Saved Views
export interface SavedView {
  id: string;
  name: string;
  filters: KentaurusIncidentFilters;
  sort_by: string;
  sort_order: 'asc' | 'desc';
  is_default: boolean;
  created_by: string;
  created_at: string;
}

// Audit Log
export interface IncidentAuditLog {
  id: string;
  incident_id: string;
  action: string;
  user: string;
  changes: Record<string, {
    old: any;
    new: any;
  }>;
  timestamp: string;
  ip_address?: string;
}
