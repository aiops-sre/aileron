# AlertHub Frontend - SRE Command Center

> Modern React.js web application providing a comprehensive SRE command center and dashboard for the AlertHub Enterprise platform.

## Overview

The SRE Command Center is the primary user interface for AlertHub Enterprise, featuring:
- Real-time alert monitoring and management dashboards
- Advanced infrastructure topology visualization
- AI-powered correlation rule builder
- Comprehensive incident management workflows
- Multi-provider integration configuration
- Advanced analytics and reporting
- Dynamic Kubernetes cluster exploration
- Enhanced observability and monitoring tools

## Architecture

```
├── src/                   # Source code
│   ├── components/       # Reusable React components
│   ├── pages/           # Page-level components
│   ├── hooks/           # Custom React hooks
│   ├── services/        # API service layer
│   ├── store/           # State management (Redux/Zustand)
│   ├── types/           # TypeScript type definitions
│   ├── utils/           # Utility functions
│   └── styles/          # CSS and styling
├── public/              # Static assets
├── docs/               # Frontend-specific documentation
├── k8s/                # Kubernetes manifests
├── Dockerfile          # Container definition
└── package.json        # Dependencies and scripts
```

## Features

### 🚨 Alert Management
- **Real-time Dashboard** - Live alert monitoring with WebSocket updates
- **Advanced Filtering** - Multi-dimensional alert filtering and search
- **Bulk Operations** - Mass acknowledgment and resolution
- **Alert Quality Metrics** - Alert noise analysis and optimization

### 🔗 Correlation & AI
- **Correlation Rule Builder** - Visual rule creation interface
- **AI Chat Integration** - Natural language incident queries
- **Pattern Recognition** - Visual correlation analysis
- **Machine Learning Insights** - AI-driven recommendations

### 🗺️ Infrastructure Topology
- **Dynamic Visualization** - Interactive infrastructure maps
- **Kubernetes Explorer** - Real-time cluster navigation
- **Service Mesh Topology** - Istio/Linkerd integration
- **Dependency Mapping** - Service relationship visualization

### 📊 Analytics & Observability
- **Performance Dashboards** - System health monitoring
- **SRE Metrics** - SLI/SLO tracking and alerting
- **Trend Analysis** - Historical data visualization
- **Custom Dashboards** - User-configurable widgets

### 🔧 Configuration Management
- **Integration Setup** - Provider configuration wizards
- **RBAC Management** - User and role administration
- **Notification Channels** - Multi-channel setup interface
- **Workflow Automation** - Visual workflow builder

## Technology Stack

- **Frontend**: React 18, TypeScript, Vite
- **UI Library**: Material-UI (MUI), Recharts, D3.js
- **State Management**: Zustand with persistence
- **API Client**: Axios with interceptors
- **Real-time**: WebSocket integration
- **Charts**: Recharts, D3.js, Cytoscape.js
- **Authentication**: OAuth2/OIDC integration
- **Testing**: Jest, React Testing Library, Cypress

## Development

### Prerequisites
- Node.js 18+
- npm 9+ or yarn 1.22+
- Docker (for containerized development)

### Local Development
```bash
# Install dependencies
npm install

# Start development server
npm run dev

# Run in development mode with hot reload
npm run dev -- --host 0.0.0.0 --port 3000

# Build for production
npm run build

# Preview production build
npm run preview

# Run tests
npm run test

# Run E2E tests
npm run test:e2e
```

### Environment Configuration
```env
# API Configuration
VITE_API_BASE_URL=http://localhost:8000
VITE_WS_URL=ws://localhost:8000/ws

# Authentication
VITE_AUTH_ENABLED=true
VITE_OAUTH_CLIENT_ID=your-client-id
VITE_OAUTH_REDIRECT_URI=http://localhost:3000/auth/callback

# Feature Flags
VITE_ENABLE_AI_CHAT=true
VITE_ENABLE_TOPOLOGY=true
VITE_ENABLE_WORKFLOWS=true

# Monitoring
VITE_SENTRY_DSN=your-sentry-dsn
VITE_ANALYTICS_ID=your-analytics-id
```

## Docker Development

```bash
# Build image
docker build -t alerthub/frontend:latest .

# Run container
docker run -p 3000:3000 \
  -e VITE_API_BASE_URL=http://api.alerthub.com \
  alerthub/frontend:latest

# Development with volume mount
docker run -p 3000:3000 -v $(pwd):/app alerthub/frontend:dev
```

## Deployment

### Kubernetes
```bash
# Deploy to Kubernetes
kubectl apply -f k8s/

# Check status
kubectl get pods -l app=alerthub-frontend

# Port forward for testing
kubectl port-forward svc/alerthub-frontend 3000:3000
```

### Production Build
```bash
# Build optimized production bundle
npm run build

# Serve static files
npm run serve

# Deploy to CDN/S3
aws s3 sync dist/ s3://your-bucket --delete
```

## Key Components

### Pages
- **DashboardPage** - Main dashboard with widgets
- **AlertsEnhancedPage** - Advanced alert management
- **AIOpsPage** - AI operations and correlation
- **IncidentsEnhancedPage** - Incident lifecycle management
- **CompleteInfrastructureTopology** - Full topology view
- **AnalyticsPage** - Metrics and reporting
- **SettingsPage** - Configuration management

### Components
- **CorrelationRuleBuilder** - Visual rule creation
- **AIProviderConfiguration** - AI service setup
- **DynamicKubernetesExplorer** - K8s cluster navigation
- **NotificationsHubPage** - Multi-channel notifications
- **ObservabilityPage** - Monitoring and metrics

## API Integration

### Authentication Flow
```typescript
// OAuth authentication
const authService = {
  login: () => redirectToOAuth(),
  logout: () => clearTokens(),
  refreshToken: () => renewAccessToken(),
  getProfile: () => fetchUserProfile()
};
```

### Real-time Updates
```typescript
// WebSocket integration
const wsClient = new WebSocketClient({
  url: process.env.VITE_WS_URL,
  reconnect: true,
  heartbeat: 30000
});

wsClient.on('alert:new', handleNewAlert);
wsClient.on('correlation:update', updateCorrelations);
```

### API Client
```typescript
// Centralized API configuration
const apiClient = axios.create({
  baseURL: process.env.VITE_API_BASE_URL,
  timeout: 10000,
  interceptors: {
    request: [authInterceptor],
    response: [errorHandler]
  }
});
```

## Performance Optimization

### Code Splitting
- Route-based lazy loading
- Component lazy loading
- Dynamic imports for heavy libraries

### Caching Strategy
- Service worker for offline support
- API response caching
- Asset caching with versioning

### Bundle Optimization
- Tree shaking for unused code
- Webpack bundle analysis
- CSS purging and minification

## Monitoring & Analytics

### Performance Monitoring
```javascript
// Performance tracking
import { getCLS, getFID, getFCP, getLCP, getTTFB } from 'web-vitals';

getCLS(sendToAnalytics);
getFID(sendToAnalytics);
getFCP(sendToAnalytics);
getLCP(sendToAnalytics);
getTTFB(sendToAnalytics);
```

### Error Tracking
- Sentry integration for error monitoring
- Custom error boundaries
- User action tracking
- Performance regression alerts

## Testing

### Unit Tests
```bash
# Run unit tests
npm run test:unit

# Watch mode
npm run test:unit:watch

# Coverage report
npm run test:coverage
```

### E2E Tests
```bash
# Run Cypress tests
npm run test:e2e

# Interactive mode
npm run test:e2e:open

# Headless CI mode
npm run test:e2e:ci
```

## Troubleshooting

### Common Issues
1. **Build Failures** - Check Node.js version and dependencies
2. **API Connection** - Verify backend service availability
3. **Authentication Issues** - Check OAuth configuration
4. **WebSocket Errors** - Verify WebSocket server connectivity

### Debug Mode
```bash
# Enable debug logging
DEBUG=alerthub:* npm run dev

# Verbose webpack output
npm run dev -- --verbose

# Analyze bundle size
npm run build:analyze
```

## Contributing

1. Follow React and TypeScript best practices
2. Use Material-UI components consistently
3. Add tests for new features and components
4. Update documentation for new pages/components
5. Ensure accessibility compliance (WCAG 2.1)

---

**Frontend Service** - Part of AlertHub Enterprise Platform
