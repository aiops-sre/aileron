# Multi-stage build for SRE Command Center React Frontend
FROM node:20-alpine AS builder

WORKDIR /app

# Use Apple internal npm registry (registry.npmjs.org not reachable from cluster)
RUN npm config set registry https://npm.example.com --global

# Ensure devDependencies are installed in the build stage
ENV NODE_ENV=development

# Copy package files
COPY package*.json ./

# Install dependencies (including devDeps — vite, typescript, etc.)
RUN npm install --legacy-peer-deps

# Copy source code
COPY . .

# Build the application
RUN npm run build

# Production stage with nginx
FROM nginx:alpine

# Copy built assets from builder
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy nginx configuration
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost/health || exit 1

CMD ["nginx", "-g", "daemon off;"]
