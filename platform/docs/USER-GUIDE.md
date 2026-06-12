# AlertHub Enterprise - User Guide

## Quick Start

### Access the Application
**URL**: https://aileron.example.com  
**Default Credentials**: 
- Username: `admin`
- Password: `Admin@123`

⚠️ **Important**: Change the default password after first login!

## Navigation

### Main Navigation Bar
The enterprise navigation bar provides quick access to all features:

- **Dashboard** - Overview of alerts, incidents, and key metrics
- **Alerts** - Manage and filter all alerts
- **Incidents** - Track and resolve incidents
- **Analytics** - View reports and trends
- **Observability** - Real-time metrics and performance
- **Integrations** - Manage external tool integrations
- **Admin** - User and system administration

### User Menu
Click your avatar in the top-right to access:
- **Settings** - Configure integrations and preferences
- **Profile** - Manage your account
- **Toggle Dark Mode** - Switch themes
- **Logout** - Sign out

## Features

### 1. Dashboard
The main dashboard provides:
- Real-time alert counts
- Open incidents
- Critical alerts
- MTTR (Mean Time To Resolution)
- Recent alerts and incidents
- SLO status

**Navigation**: Click any stat card to drill down into details

### 2. Alerts Management

**Access**: Navigate to "Alerts" from main menu

**Features**:
- Filter by severity (Critical, High, Medium, Low)
- Filter by status (Open, Acknowledged, Resolved)
- Filter by source (Prometheus, Dynatrace, etc.)
- Search alerts by title or description
- Acknowledge alerts
- Resolve alerts
- Assign alerts to team members
- Auto-refresh every 30 seconds

**Actions**:
- Click an alert card to view full details
- Use action buttons to acknowledge or resolve
- Assign alerts to users for investigation

### 3. Incident Management

**Access**: Navigate to "Incidents" from main menu

**Features**:
- View all incidents with detailed information
- Filter by severity and status
- Track incident timeline
- Perform Root Cause Analysis (RCA)
- Monitor incident duration
- Automatic alert correlation

**Incident Lifecycle**:
1. **Open** - Incident created
2. **Investigating** - Team is investigating
3. **Resolved** - Incident resolved with RCA

### 4. Analytics & Reports

**Access**: Navigate to "Analytics" from main menu

**Available Reports**:
- **Overview** - 30-day summary with key metrics
- **Alert Analytics** - Alert trends and patterns
- **Incident Analytics** - Incident resolution metrics
- **SLO Performance** - Service level objective tracking
- **Custom Reports** - Build custom reports

**Metrics Tracked**:
- Total alerts
- Mean Time To Resolution (MTTR)
- Resolution rate
- SLO compliance
- Alerts by severity
- Alerts by source

**Export**: Click "Export Report" to download reports (PDF/CSV)

### 5. Observability Dashboard

**Access**: Navigate to "Observability" from main menu

**Features**:
- **Metrics Tab** - System and application metrics
  - CPU usage with health indicators
  - Memory usage trends
  - Request rate monitoring
  - Response time tracking
  - Interactive charts with time range selection
  
- **Traces Tab** - Distributed tracing (when configured)
- **Logs Tab** - Centralized log aggregation
- **APM Tab** - Application performance monitoring

**Capabilities**:
- Real-time metric updates every 30 seconds
- Historical trend analysis
- Health status indicators (Healthy/Warning/Critical)
- Custom time ranges (1h, 6h, 24h, 7d)

### 6. Integrations

**Access**: Navigate to "Integrations" from main menu or Settings

#### Monitoring Integrations

**Dynatrace**
- Import problems as incidents
- Bidirectional status sync
- Metrics visualization
- Event creation

**Configuration**: Settings → Monitoring → Dynatrace
1. Enable integration
2. Enter environment URL
3. Add API token
4. Configure sync interval
5. Select features to enable
6. Test connection
7. Save

**Prometheus**
- Import Prometheus alerts
- Query metrics with PromQL
- Export AlertHub metrics
- Custom alert rules

**Configuration**: Settings → Monitoring → Prometheus
1. Enable integration
2. Enter Prometheus URL
3. (Optional) AlertManager URL
4. Define custom PromQL rules
5. Test connection
6. Save

**Grafana**
- Embed dashboards
- Create incident annotations
- Import Grafana alerts
- Deep linking

**Configuration**: Settings → Monitoring → Grafana
1. Enable integration
2. Enter Grafana URL
3. Add API key
4. Set default dashboard UID
5. Enable desired features
6. Test connection
7. Save

**Datadog**
- Sync monitors and events
- Full-stack monitoring
- APM correlation

**Configuration**: Settings → Monitoring → Datadog
1. Enable integration
2. Enter API key
3. Enter Application key
4. Select site region
5. Test connection
6. Save

#### Notification Integrations

**Slack** - Team notifications
**PagerDuty** - On-call alerts
**Email (SMTP)** - Email notifications

**Configuration**: Settings → respective tabs

#### Ticketing Integrations

**Jira** - Automatic ticket creation
**ServiceNow** - ITSM integration

### 7. Admin Panel

**Access**: Navigate to "Admin" from main menu (Admin role required)

#### User Management

**Features**:
- View all users with status
- Create new users
- Edit user details
- Delete users
- Assign roles
- Activate/deactivate accounts

**Actions**:
- Click "Create User" to add new user
- Click "Edit" to modify user details
- Click "Delete" to remove user (confirmation required)

**User Fields**:
- Username (required)
- Email (required)
- Full Name
- Password (required for new users)
- Role assignment
- Active status

#### Roles & Permissions

**Features**:
- View all roles
- Create custom roles
- Edit permissions
- System roles (protected)

**Permission Categories**:
- Users (view, create, update, delete)
- Roles (view, manage)
- Alerts (view, create, update, resolve)
- Incidents (view, create, manage)
- Analytics (view)
- AI (use)
- Audit (view)

#### API Keys

**Features**:
- Generate API keys for external access
- Set expiration periods
- Revoke keys
- Track usage

#### Audit Logs

**Features**:
- View all system actions
- Filter by user, action, resource
- Export audit logs
- Compliance reporting

**Logged Actions**:
- User login/logout
- Configuration changes
- Alert/incident modifications
- Role assignments
- Permission changes

### 8. Settings

**Access**: User Menu → Settings

#### System Configuration

**Available Settings**:
1. **Monitoring** - Configure monitoring tool integrations
2. **LDAP/AD** - Enterprise authentication
3. **SAML SSO** - Single sign-on
4. **OAuth** - Third-party authentication
5. **Email (SMTP)** - Email notifications
6. **Slack** - Slack notifications
7. **AI Integration** - AI-powered features
8. **General** - System-wide settings

## Keyboard Shortcuts

- `Cmd/Ctrl + K` - Global search
- `Esc` - Close modals/dropdowns
- `R` - Refresh current view

## Best Practices

### Alert Management
1. Acknowledge alerts promptly
2. Investigate and resolve within SLO
3. Use AI classification for categorization
4. Assign alerts to appropriate team members

### Incident Management
1. Create incidents from related alerts
2. Document investigation steps
3. Perform RCA before closing
4. Update status regularly

### Integration Setup
1. Configure monitoring tools first (Dynatrace, Prometheus, Grafana)
2. Test connections before enabling
3. Set appropriate sync intervals
4. Monitor integration logs

### User Management
1. Follow least-privilege principle
2. Use LDAP/SSO for enterprise authentication
3. Regular audit log reviews
4. Enforce strong password policies

## Troubleshooting

### Cannot Login
- Verify credentials (case-sensitive)
- Check account is active
- Clear browser cache/cookies
- Try incognito mode

### Missing Data
- Check integration configurations
- Verify API tokens are valid
- Review sync intervals
- Check audit logs for errors

### Performance Issues
- Adjust auto-refresh intervals
- Use filters to limit data
- Check observability metrics
- Review backend logs

## Integration-Specific Guides

### Dynatrace Setup
1. In Dynatrace, create API token with:
   - `problems.read`
   - `events.ingest`
   - `metrics.read`
2. Copy environment URL and token
3. Configure in AlertHub Settings
4. Enable desired features
5. Set sync interval (recommended: 15 minutes)

### Prometheus Setup
1. Ensure Prometheus is accessible
2. Obtain Prometheus URL
3. Configure in AlertHub
4. Add custom PromQL rules if needed
5. Enable metrics export endpoint: `/metrics`

### Grafana Setup
1. Create API key in Grafana with Editor role
2. Note default dashboard UID
3. Configure in AlertHub
4. Enable dashboard embedding
5. Enable incident annotations

## API Access

### Generate API Key
1. Navigate to Admin → Integrations tab
2. Click "Generate API Key"
3. Name your key
4. Set expiration
5. Copy and securely store the generated key

### Using API Keys
```bash
curl -H "Authorization: Bearer YOUR_API_KEY" \
     https://aileron.example.com/api/v1/alerts
```

## Security

### Password Policy
- Minimum 8 characters
- Mix of letters, numbers, symbols recommended
- Change default passwords immediately

### Session Management
- Sessions expire after 24 hours
- Automatic logout on inactivity
- Multiple device sessions supported

### MFA (Multi-Factor Authentication)
- Available in Settings → Security
- Supports TOTP apps (Google Authenticator, Authy)

## Support

### Getting Help
- **Slack**: #help-interactive-sre
- **Wiki**: https://wiki.iapps.example.com/display/ISRE
- **Email**: interactive-sre@apple.com

### Reporting Issues
Include:
- Steps to reproduce
- Expected vs actual behavior
- Screenshots if applicable
- Browser and version

## Updates & Releases

Current Version: **t1.0.11 (Backend)** | **t1.0.5 (Frontend)**

### Recent Features
- ✅ Enterprise navigation system
- ✅ Monitoring integrations (Dynatrace, Prometheus, Grafana, Datadog)
- ✅ Observability dashboard
- ✅ Enhanced alert management
- ✅ Incident tracking with RCA
- ✅ Analytics & reporting
- ✅ User management with CRUD operations
- ✅ Integration marketplace

### Upcoming Features
- WebSocket real-time updates
- Advanced export capabilities
- Mobile app
- Custom dashboard builder
- ML-powered predictions

---

**Last Updated**: 2026-01-17  
**Documentation Version**: 1.0
