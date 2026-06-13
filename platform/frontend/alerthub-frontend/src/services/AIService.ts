// AI Service - Advanced Backend API Integration
// Enhanced token management matching frontend-3.0 standards

import { corporateOAuthService } from './CorporateOAuthService'

interface ChatMessage {
  role: 'user' | 'assistant' | 'system';
  content: string;
}

interface AIModel {
  id: string;
  name: string;
  provider: string;
  description?: string;
  capabilities?: string[];
  context_length?: number;
}

interface ChatSession {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  message_count: number;
  model?: string;
  metadata?: Record<string, any>;
}

interface ApiResponse<T = any> {
  success: boolean;
  data?: T;
  message?: string;
  error?: string;
  code?: string;
}

interface ChatResponse {
  content: string;
  model?: string;
  session_id?: string;
  usage?: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
}

interface TokenInfo {
  token: string;
  expiry?: string;
  source: 'manual' | 'auto' | 'oauth' | 'sso';
  scopes?: string[];
  user?: string;
  created_at?: string;
}

type TokenStatus = 'valid' | 'expiring' | 'expired' | 'invalid' | 'missing';

class AIService {
  private baseUrl = '/api/v1';
  private maxRetries = 3;
  private retryDelay = 1000;
  private tokenCheckInterval: NodeJS.Timeout | null = null;
  private tokenRefreshPromise: Promise<boolean> | null = null;
  private listeners: Set<(status: TokenStatus) => void> = new Set();

  constructor() {
    this.startTokenMonitoring();
    this.attemptAutoTokenDetection();
  }

  // Enhanced authentication headers with multiple token sources
  private getAuthHeaders(): Record<string, string> {
    const accessToken = this.getAccessToken();
    const oidcToken = this.getOIDC ProviderToken();
    const oauthToken = localStorage.getItem('oauth_id_token');
    
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'X-Client-Version': '3.0',
      'X-Request-ID': this.generateRequestId(),
    };

    if (accessToken) {
      headers['Authorization'] = `Bearer ${accessToken}`;
    }

    if (oidcToken) {
      headers['X-OIDC Provider-Token'] = oidcToken;
    }

    if (oauthToken) {
      headers['X-OAuth-Token'] = oauthToken;
    }

    // Add user context if available
    const user = JSON.parse(localStorage.getItem('user') || '{}');
    if (user.id) {
      headers['X-User-ID'] = user.id;
    }

    return headers;
  }

  private generateRequestId(): string {
    return `req_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
  }

  private getAccessToken(): string {
    return sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || localStorage.getItem('token') || '';
  }

  // Advanced OIDC Provider token management
  private getOIDC ProviderToken(): string {
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    if (!tokenInfo) return '';

    const status = this.getTokenStatus(tokenInfo);
    if (status === 'valid') {
      return tokenInfo.token;
    }

    // Try to refresh if expiring
    if (status === 'expiring') {
      this.scheduleTokenRefresh();
      return tokenInfo.token; // Still usable
    }

    return '';
  }

  private getOIDC ProviderTokenInfo(): TokenInfo | null {
    const token = localStorage.getItem('oidc_token');
    const expiry = localStorage.getItem('oidc_token_expiry');
    const source = localStorage.getItem('oidc_token_source') || 'manual';
    const createdAt = localStorage.getItem('oidc_token_created');

    if (!token) return null;

    return {
      token,
      expiry: expiry || undefined,
      source: source as TokenInfo['source'],
      created_at: createdAt || new Date().toISOString(),
    };
  }

  private getTokenStatus(tokenInfo: TokenInfo): TokenStatus {
    if (!tokenInfo.token) return 'missing';
    
    if (!tokenInfo.expiry) return 'valid'; // No expiry means long-lived

    const expiryTime = new Date(tokenInfo.expiry).getTime();
    const now = Date.now();
    const fiveMinutes = 5 * 60 * 1000;
    const oneHour = 60 * 60 * 1000;

    if (now >= expiryTime) return 'expired';
    if (now >= expiryTime - fiveMinutes) return 'expiring';
    if (now >= expiryTime - oneHour) return 'valid'; // Show warning in UI later

    return 'valid';
  }

  // Enhanced token validation
  isOIDC ProviderTokenValid(): boolean {
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    if (!tokenInfo) return false;

    const status = this.getTokenStatus(tokenInfo);
    return status === 'valid' || status === 'expiring';
  }

  // Advanced token setting with metadata
  setOIDC ProviderToken(
    token: string,
    expiryDate?: string,
    source: TokenInfo['source'] = 'manual',
    metadata?: Record<string, any>
  ) {
    const now = new Date().toISOString();
    
    localStorage.setItem('oidc_token', token);
    localStorage.setItem('oidc_token_source', source);
    localStorage.setItem('oidc_token_created', now);
    
    if (expiryDate) {
      localStorage.setItem('oidc_token_expiry', expiryDate);
    }

    if (metadata) {
      localStorage.setItem('oidc_token_metadata', JSON.stringify(metadata));
    }

    // Notify listeners
    this.notifyTokenStatusChange('valid');
    
    // Schedule expiry check
    this.scheduleTokenExpiryCheck();
  }

  // Token monitoring and auto-refresh
  private startTokenMonitoring() {
    if (this.tokenCheckInterval) {
      clearInterval(this.tokenCheckInterval);
    }

    this.tokenCheckInterval = setInterval(() => {
      this.checkTokenHealth();
    }, 30000); // Check every 30 seconds
  }

  private checkTokenHealth() {
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    if (!tokenInfo) {
      this.notifyTokenStatusChange('missing');
      return;
    }

    const status = this.getTokenStatus(tokenInfo);
    this.notifyTokenStatusChange(status);

    // Auto-refresh if expiring
    if (status === 'expiring' && !this.tokenRefreshPromise) {
      this.attemptTokenRefresh();
    }
  }

  private async attemptTokenRefresh(): Promise<boolean> {
    if (this.tokenRefreshPromise) {
      return this.tokenRefreshPromise;
    }

    this.tokenRefreshPromise = this.performTokenRefresh();
    const result = await this.tokenRefreshPromise;
    this.tokenRefreshPromise = null;
    
    return result;
  }

  private async performTokenRefresh(): Promise<boolean> {
    try {
      const response = await fetch(`${this.baseUrl}/oauth/refresh`, {
        method: 'POST',
        headers: this.getAuthHeaders(),
      });

      if (response.ok) {
        const data = await response.json();
        if (data.success && data.data?.token) {
          this.setOIDC ProviderToken(
            data.data.token,
            data.data.expiry,
            'auto',
            { refreshed_at: new Date().toISOString() }
          );
          return true;
        }
      }
    } catch (error) {
      console.error('Token refresh failed:', error);
    }
    
    return false;
  }

  private scheduleTokenRefresh() {
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    if (!tokenInfo?.expiry) return;

    const expiryTime = new Date(tokenInfo.expiry).getTime();
    const now = Date.now();
    const tenMinutes = 10 * 60 * 1000;
    const refreshTime = Math.max(0, expiryTime - now - tenMinutes);

    setTimeout(() => {
      this.attemptTokenRefresh();
    }, refreshTime);
  }

  private scheduleTokenExpiryCheck() {
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    if (!tokenInfo?.expiry) return;

    const expiryTime = new Date(tokenInfo.expiry).getTime();
    const now = Date.now();
    const fiveMinutes = 5 * 60 * 1000;
    const warningTime = Math.max(0, expiryTime - now - fiveMinutes);

    setTimeout(() => {
      this.notifyTokenStatusChange('expiring');
    }, warningTime);
  }

  // Auto-detection of tokens from various sources
  private async attemptAutoTokenDetection() {
    // Try to get token from OAuth flow
    const urlParams = new URLSearchParams(window.location.search);
    const oidcParam = urlParams.get('oidc_token');
    
    if (oidcParam) {
      const expiry = urlParams.get('token_expiry');
      this.setOIDC ProviderToken(oidcParam, expiry || undefined, 'oauth');
      
      // Clean up URL
      const newUrl = new URL(window.location.href);
      newUrl.searchParams.delete('oidc_token');
      newUrl.searchParams.delete('token_expiry');
      window.history.replaceState({}, '', newUrl.toString());
    }
  }

  // Token status monitoring
  onTokenStatusChange(callback: (status: TokenStatus) => void) {
    this.listeners.add(callback);
    
    // Immediately notify current status
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    const status = tokenInfo ? this.getTokenStatus(tokenInfo) : 'missing';
    callback(status);
    
    return () => {
      this.listeners.delete(callback);
    };
  }

  private notifyTokenStatusChange(status: TokenStatus) {
    this.listeners.forEach(callback => callback(status));
  }

  getCurrentTokenInfo(): TokenInfo | null {
    return this.getOIDC ProviderTokenInfo();
  }

  getTokenTimeRemaining(): number | null {
    const tokenInfo = this.getOIDC ProviderTokenInfo();
    if (!tokenInfo?.expiry) return null;

    const expiryTime = new Date(tokenInfo.expiry).getTime();
    const now = Date.now();
    
    return Math.max(0, expiryTime - now);
  }

  // Clean up resources
  destroy() {
    if (this.tokenCheckInterval) {
      clearInterval(this.tokenCheckInterval);
    }
    this.listeners.clear();
  }

  // Fetch with retry logic
  private async fetchWithRetry<T>(
    url: string,
    options: RequestInit = {},
    retries = this.maxRetries
  ): Promise<T> {
    for (let i = 0; i < retries; i++) {
      try {
        if (i > 0) {
          // Exponential backoff
          await new Promise(resolve => 
            setTimeout(resolve, this.retryDelay * Math.pow(2, i - 1))
          );
        }

        const response = await fetch(url, {
          ...options,
          headers: {
            ...this.getAuthHeaders(),
            ...options.headers,
          },
        });

        if (!response.ok) {
          const errorData = await response.json().catch(() => ({}));
          throw new Error(errorData.message || errorData.error || `HTTP ${response.status}`);
        }

        return await response.json();
      } catch (error: any) {
        console.error(`Attempt ${i + 1}/${retries} failed:`, error);
        
        // Don't retry on authentication errors
        if (error.message?.includes('401') || error.message?.includes('403')) {
          throw error;
        }

        // Last attempt, throw error
        if (i === retries - 1) {
          throw error;
        }
      }
    }

    throw new Error('Max retries exceeded');
  }

  // Get available AI models from OIDC Provider via Corporate OAuth proxy
  async getModels(): Promise<AIModel[]> {
    try {
      // Try Corporate OAuth + OIDC Provider first
      if (corporateOAuthService.hasValidMultiAudienceToken()) {
        console.log('🔑 Using Corporate OAuth + OIDC Provider for models')
        const oidcResponse = await corporateOAuthService.getOIDC ProviderModels()
        
        // Transform OIDC Provider response to our format
        if (oidcResponse && Array.isArray(oidcResponse)) {
          return oidcResponse.map((m: any) => ({
            id: m.id,
            name: m.id,
            provider: 'oidc',
            description: m.id,
            capabilities: ['chat', 'completion'],
            context_length: m.context_length || 8192,
          }))
        }
      }

      // Fallback to backend AI service
      console.log('🔄 Using backend AI service for models')
      const response = await this.fetchWithRetry<ApiResponse<{ models: AIModel[] }>>(
        `${this.baseUrl}/ai/models`
      );

      if (response.success && response.data?.models) {
        return response.data.models;
      }

      return [];
    } catch (error) {
      console.error('Failed to load models:', error);
      return [];
    }
  }

  // Get chat sessions
  async getSessions(): Promise<ChatSession[]> {
    try {
      const response = await this.fetchWithRetry<ApiResponse<{ sessions: ChatSession[] }>>(
        `${this.baseUrl}/ai/sessions`
      );

      if (response.success && response.data?.sessions) {
        return response.data.sessions;
      }

      return [];
    } catch (error) {
      console.error('Failed to load sessions:', error);
      return [];
    }
  }

  // Get messages for a session
  async getSessionMessages(sessionId: string): Promise<ChatMessage[]> {
    try {
      const response = await this.fetchWithRetry<ApiResponse<{ messages: ChatMessage[] }>>(
        `${this.baseUrl}/ai/sessions/${sessionId}/messages`
      );

      if (response.success && response.data?.messages) {
        return response.data.messages;
      }

      return [];
    } catch (error) {
      console.error('Failed to load session messages:', error);
      return [];
    }
  }

  // Delete a session
  async deleteSession(sessionId: string): Promise<boolean> {
    try {
      const response = await this.fetchWithRetry<ApiResponse>(
        `${this.baseUrl}/ai/sessions/${sessionId}`,
        {
          method: 'DELETE',
        }
      );

      return response.success;
    } catch (error) {
      console.error('Failed to delete session:', error);
      return false;
    }
  }

  // Send chat message via Corporate OAuth + OIDC Provider or backend
  async sendMessage(
    messages: ChatMessage[],
    model?: string,
    sessionId?: string
  ): Promise<ChatResponse> {
    try {
      // Try Corporate OAuth + OIDC Provider first for better models
      if (corporateOAuthService.hasValidMultiAudienceToken() && model && (model.startsWith('gpt') || model.startsWith('claude') || model.startsWith('gcp:'))) {
        console.log('🔑 Using Corporate OAuth + OIDC Provider for chat')
        
        const oidcResponse = await corporateOAuthService.chatWithOIDC Provider(messages, model)
        
        // Transform OIDC Provider response to our format
        if (oidcResponse?.choices?.[0]?.message) {
          return {
            content: oidcResponse.choices[0].message.content,
            model: model,
            session_id: sessionId,
            usage: oidcResponse.usage,
          }
        }
      }

      // Fallback to backend AI service
      console.log('🔄 Using backend AI service for chat')
      
      const payload: any = {
        messages: messages,
      };

      if (model) {
        payload.model = model;
      }

      if (sessionId) {
        payload.session_id = sessionId;
      }

      const response = await this.fetchWithRetry<ApiResponse<ChatResponse>>(
        `${this.baseUrl}/ai/chat`,
        {
          method: 'POST',
          body: JSON.stringify(payload),
        }
      );

      if (response.success && response.data) {
        return response.data;
      }

      throw new Error(response.message || 'Failed to get AI response');
    } catch (error: any) {
      console.error('Chat API error:', error);
      throw error;
    }
  }

  // Create a new session
  async createSession(title: string = 'New Conversation'): Promise<ChatSession | null> {
    try {
      const response = await this.fetchWithRetry<ApiResponse<{ session: ChatSession }>>(
        `${this.baseUrl}/ai/sessions`,
        {
          method: 'POST',
          body: JSON.stringify({ title }),
        }
      );

      if (response.success && response.data?.session) {
        return response.data.session;
      }

      return null;
    } catch (error) {
      console.error('Failed to create session:', error);
      return null;
    }
  }

  // Update session title
  async updateSessionTitle(sessionId: string, title: string): Promise<boolean> {
    try {
      const response = await this.fetchWithRetry<ApiResponse>(
        `${this.baseUrl}/ai/sessions/${sessionId}`,
        {
          method: 'PATCH',
          body: JSON.stringify({ title }),
        }
      );

      return response.success;
    } catch (error) {
      console.error('Failed to update session title:', error);
      return false;
    }
  }

  // Generate title for conversation
  async generateTitle(firstMessage: string): Promise<string> {
    // Simple client-side title generation
    const words = firstMessage.trim().split(' ').slice(0, 6);
    let title = words.join(' ');
    if (firstMessage.trim().split(' ').length > 6) {
      title += '...';
    }
    return title || 'New Conversation';
  }

  // Health check for AI service
  async healthCheck(): Promise<boolean> {
    try {
      const response = await fetch(`${this.baseUrl}/health`, {
        headers: this.getAuthHeaders(),
      });
      return response.ok;
    } catch (error) {
      return false;
    }
  }

  // Initialize Corporate OAuth for OIDC Provider access
  async initializeCorporateOAuth(): Promise<boolean> {
    try {
      // Check if we already have a valid multi-audience token
      if (corporateOAuthService.hasValidMultiAudienceToken()) {
        console.log('✅ Corporate OAuth already initialized')
        return true
      }

      // Get MAS assertion from localStorage or ingress headers
      const masAssertion = localStorage.getItem('mas_assertion') ||
                          localStorage.getItem('oauth_id_token') ||
                          sessionStorage.getItem('access_token') ||
                          localStorage.getItem('access_token')

      if (!masAssertion) {
        console.warn('⚠️ No MAS assertion available for Corporate OAuth')
        return false
      }

      // Generate multi-audience token
      console.log('🔑 Generating multi-audience token for OIDC Provider access...')
      await corporateOAuthService.generateMultiAudienceToken(masAssertion)
      
      console.log('✅ Corporate OAuth initialized - OIDC Provider access enabled')
      return true

    } catch (error) {
      console.error('❌ Corporate OAuth initialization failed:', error)
      return false
    }
  }

  // Check if OIDC Provider is available via Corporate OAuth
  isOIDC ProviderAvailable(): boolean {
    return corporateOAuthService.hasValidMultiAudienceToken()
  }
}

// Export singleton instance
export const aiService = new AIService();
export type { ChatMessage, AIModel, ChatSession, ChatResponse };
