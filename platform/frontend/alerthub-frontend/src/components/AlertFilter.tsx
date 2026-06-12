import React from 'react';
import type { Alert, AlertFilters } from '@/types';

interface AlertFilterProps {
  alerts: Alert[];
  onFilterChange: (filteredAlerts: Alert[]) => void;
}

interface FilterPreset {
  name: string;
  icon: string;
  description: string;
  filters: Partial<AlertFilters>;
}

export class AlertFilterService {
  public alerts: Alert[];
  private options: any;
  private filters: Partial<AlertFilters>;
  private presets: Record<string, FilterPreset>;

  constructor(alerts: Alert[] = [], options: any = {}) {
    this.alerts = alerts;
    this.options = {
      onFilterChange: options.onFilterChange || (() => {}),
      ...options
    };
    
    this.filters = {
      search: '',
      severity: '',
      status: '',
      time_range: '',
      source: '',
    };

    this.presets = {
      'my-alerts': {
        name: 'My Alerts',
        icon: 'fas fa-user',
        description: 'Only alerts assigned to me',
        filters: {}
      },
      'needs-action': {
        name: 'Needs Action', 
        icon: 'fas fa-exclamation-circle',
        description: 'Open and unassigned alerts',
        filters: { status: 'open' }
      },
      'critical': {
        name: 'Critical Only',
        icon: 'fas fa-fire',
        description: 'Critical severity alerts',
        filters: { severity: 'critical' }
      },
      'recent': {
        name: 'Recent',
        icon: 'fas fa-clock', 
        description: 'Alerts from last hour',
        filters: { time_range: '1h' }
      }
    };
  }

  setFilter(filterName: keyof AlertFilters, value: string) {
    (this.filters as any)[filterName] = value;
    this.notifyChange();
  }

  setFilters(filterObj: Partial<AlertFilters>) {
    this.filters = { ...this.filters, ...filterObj };
    this.notifyChange();
  }

  applyPreset(presetKey: string) {
    if (this.presets[presetKey]) {
      this.filters = { ...this.filters, ...this.presets[presetKey].filters };
      this.notifyChange();
    }
  }

  clearFilters() {
    this.filters = {
      search: '',
      severity: '',
      status: '',
      time_range: '',
      source: '',
    };
    this.notifyChange();
  }

  getActiveFilters() {
    return Object.entries(this.filters).filter(([_, value]) => value !== '');
  }

  getActiveFilterCount() {
    return this.getActiveFilters().length;
  }

  filterAlerts(alerts = this.alerts): Alert[] {
    return alerts.filter(alert => {
      // Search filter
      if (this.filters.search) {
        const searchTerm = this.filters.search.toLowerCase();
        const matchesSearch = 
          alert.title.toLowerCase().includes(searchTerm) ||
          (alert.description && alert.description.toLowerCase().includes(searchTerm));
        if (!matchesSearch) return false;
      }

      // Severity filter
      if (this.filters.severity && alert.severity !== this.filters.severity) {
        return false;
      }

      // Status filter
      if (this.filters.status && alert.status !== this.filters.status) {
        return false;
      }

      // Time filter
      if (this.filters.time_range) {
        const alertTime = new Date(alert.created_at);
        const now = new Date();
        const diff = now.getTime() - alertTime.getTime();
        
        const timeRanges: Record<string, number> = {
          '1h': 3600000,
          '24h': 86400000,
          '7d': 604800000
        };

        if (diff > timeRanges[this.filters.time_range]) {
          return false;
        }
      }

      // Source filter
      if (this.filters.source && alert.source !== this.filters.source) {
        return false;
      }

      return true;
    });
  }

  private notifyChange() {
    this.options.onFilterChange(this.filterAlerts());
  }

  getFilterLabel(filterName: keyof AlertFilters) {
    const labels: Record<string, string> = {
      search: 'Search',
      severity: 'Severity',
      status: 'Status',
      time_range: 'Time',
      source: 'Source',
      tags: 'Tags'
    };
    return labels[filterName] || filterName;
  }

  getSeverityOptions() {
    return [
      { value: '', label: 'All' },
      { value: 'critical', label: 'Critical' },
      { value: 'high', label: 'High' },
      { value: 'medium', label: 'Medium' },
      { value: 'low', label: 'Low' }
    ];
  }

  getStatusOptions() {
    return [
      { value: '', label: 'All' },
      { value: 'open', label: 'Open' },
      { value: 'acknowledged', label: 'Acknowledged' },
      { value: 'investigating', label: 'Investigating' },
      { value: 'resolved', label: 'Resolved' }
    ];
  }

  getTimeOptions() {
    return [
      { value: '', label: 'All Time' },
      { value: '1h', label: 'Last Hour' },
      { value: '24h', label: 'Last 24h' },
      { value: '7d', label: 'Last 7 Days' }
    ];
  }

  getSourceOptions() {
    const sources = new Set(this.alerts.map(a => a.source).filter(Boolean));
    return [
      { value: '', label: 'All Sources' },
      ...Array.from(sources).map(source => ({
        value: source,
        label: source.charAt(0).toUpperCase() + source.slice(1)
      }))
    ];
  }

  getFilters() {
    return { ...this.filters };
  }

  getPresets() {
    return { ...this.presets };
  }
}

export function AlertFilter({ alerts, onFilterChange }: AlertFilterProps) {
  const [filterService] = React.useState(() => new AlertFilterService(alerts, { onFilterChange }));
  const [filters, setFilters] = React.useState(() => filterService.getFilters());

  React.useEffect(() => {
    filterService.alerts = alerts;
    const filtered = filterService.filterAlerts();
    onFilterChange(filtered);
  }, [alerts, filterService, onFilterChange]);

  const handleFilterChange = (filterName: keyof AlertFilters, value: string) => {
    filterService.setFilter(filterName, value);
    setFilters(filterService.getFilters());
  };

  const handlePresetApply = (presetKey: string) => {
    filterService.applyPreset(presetKey);
    setFilters(filterService.getFilters());
  };

  const handleClearFilters = () => {
    filterService.clearFilters();
    setFilters(filterService.getFilters());
  };

  const severityOptions = filterService.getSeverityOptions();
  const statusOptions = filterService.getStatusOptions();
  const timeOptions = filterService.getTimeOptions();
  const sourceOptions = filterService.getSourceOptions();
  const presets = filterService.getPresets();
  const activeFilters = filterService.getActiveFilters();

  return (
    <div>
      <div className="filter-bar" style={{
        display: 'flex',
        gap: '12px',
        alignItems: 'center',
        marginBottom: '16px',
        flexWrap: 'wrap'
      }}>
        <div className="filter-group">
          <span className="filter-label" style={{
            fontSize: '12px',
            fontWeight: 600,
            color: 'var(--color-text-secondary)',
            marginRight: '8px'
          }}>
            Severity:
          </span>
          <select 
            className="filter-select"
            value={filters.severity || ''}
            onChange={(e) => handleFilterChange('severity', e.target.value)}
            style={{
              padding: '8px 12px',
              border: '1px solid var(--color-separator)',
              borderRadius: '6px',
              background: 'var(--color-background)',
              color: 'var(--color-text)',
              fontSize: '13px'
            }}
          >
            {severityOptions.map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </div>

        <div className="filter-group">
          <span className="filter-label" style={{
            fontSize: '12px',
            fontWeight: 600,
            color: 'var(--color-text-secondary)',
            marginRight: '8px'
          }}>
            Status:
          </span>
          <select 
            className="filter-select"
            value={filters.status || ''}
            onChange={(e) => handleFilterChange('status', e.target.value)}
            style={{
              padding: '8px 12px',
              border: '1px solid var(--color-separator)',
              borderRadius: '6px',
              background: 'var(--color-background)',
              color: 'var(--color-text)',
              fontSize: '13px'
            }}
          >
            {statusOptions.map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </div>

        <div className="filter-group">
          <span className="filter-label" style={{
            fontSize: '12px',
            fontWeight: 600,
            color: 'var(--color-text-secondary)',
            marginRight: '8px'
          }}>
            Time:
          </span>
          <select 
            className="filter-select"
            value={filters.time_range || ''}
            onChange={(e) => handleFilterChange('time_range', e.target.value)}
            style={{
              padding: '8px 12px',
              border: '1px solid var(--color-separator)',
              borderRadius: '6px',
              background: 'var(--color-background)',
              color: 'var(--color-text)',
              fontSize: '13px'
            }}
          >
            {timeOptions.map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </div>

        <div className="filter-group">
          <span className="filter-label" style={{
            fontSize: '12px',
            fontWeight: 600,
            color: 'var(--color-text-secondary)',
            marginRight: '8px'
          }}>
            Source:
          </span>
          <select 
            className="filter-select"
            value={filters.source || ''}
            onChange={(e) => handleFilterChange('source', e.target.value)}
            style={{
              padding: '8px 12px',
              border: '1px solid var(--color-separator)',
              borderRadius: '6px',
              background: 'var(--color-background)',
              color: 'var(--color-text)',
              fontSize: '13px'
            }}
          >
            {sourceOptions.map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        </div>
      </div>

      <div className="quick-presets" style={{
        display: 'flex',
        gap: '8px',
        marginBottom: '16px',
        flexWrap: 'wrap'
      }}>
        {Object.entries(presets).map(([key, preset]) => (
          <button 
            key={key}
            className="preset-btn" 
            onClick={() => handlePresetApply(key)}
            title={preset.description}
            style={{
              padding: '6px 12px',
              border: '1px solid var(--color-separator)',
              borderRadius: '6px',
              background: 'var(--color-background)',
              color: 'var(--color-text)',
              fontSize: '12px',
              fontWeight: 500,
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: '4px',
              transition: 'all 0.2s'
            }}
          >
            <i className={preset.icon}></i> {preset.name}
          </button>
        ))}
        <button 
          className="preset-btn" 
          onClick={handleClearFilters}
          title="Clear all filters"
          style={{
            padding: '6px 12px',
            border: '1px solid var(--color-separator)',
            borderRadius: '6px',
            background: 'var(--color-background)',
            color: 'var(--color-text)',
            fontSize: '12px',
            fontWeight: 500,
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '4px',
            transition: 'all 0.2s'
          }}
        >
          <i className="fas fa-times"></i> Clear
        </button>
      </div>

      {activeFilters.length > 0 && (
        <div className="active-filters" style={{
          display: 'flex',
          gap: '8px',
          marginBottom: '16px',
          flexWrap: 'wrap'
        }}>
          {activeFilters.map(([name, value]) => (
            <span 
              key={name}
              className="filter-pill active"
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: '6px',
                padding: '4px 12px',
                background: 'var(--color-blue)',
                color: '#fff',
                borderRadius: '12px',
                fontSize: '12px',
                fontWeight: 500
              }}
            >
              {filterService.getFilterLabel(name as keyof AlertFilters)}: {value}
              <button 
                onClick={() => handleFilterChange(name as keyof AlertFilters, '')}
                className="filter-pill-close"
                style={{
                  background: 'none',
                  border: 'none',
                  color: '#fff',
                  cursor: 'pointer',
                  fontSize: '14px',
                  padding: '0',
                  marginLeft: '4px'
                }}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}