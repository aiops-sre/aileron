/**
 * Corporate OAuth 2.0 Service
 * Handles multi-audience token generation and Floodgate proxy integration
 */

interface OAuthTokenResponse {
  access_token: string
  refresh_token?: string
  token_type: string
  expires_in: number
  scope?: string
}

interface FloodgateProxyRequest {
  method: string
  path: string
  headers?: Record<string, string>
  body?: any
}

class CorporateOAuthService {
  private baseURL = '/api/v1'
  private tokenCache: Map<string, { token: OAuthTokenResponse; expiresAt: number }> = new Map()

  /**
   * Generate multi-audience token using JWT assertion
   * This token is valid for both AlertHub AND Floodgate
   */
  async generateMultiAudienceToken(assertion: string): Promise<OAuthTokenResponse> {
    try {
      const response = await fetch(`${this.baseURL}/oauth/token`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${this.getAuthToken()}`,
        },
        body: JSON.stringify({ assertion }),
      })

      if (!response.ok) {
        const error = await response.json()
        throw new Error(error.message || `HTTP ${response.status}`)
      }

      const data = await response.json()

      if (!data.success || !data.data) {
        throw new Error('Invalid token response')
      }

      const token = data.data as OAuthTokenResponse

      // Cache token
      this.cacheToken('multi-audience', token)

      console.log('✅ Multi-audience token generated (valid for AlertHub + Floodgate)')
      return token

    } catch (error: any) {
      console.error('❌ Multi-audience token generation failed:', error)
      throw new Error(`Token generation failed: ${error.message}`)
    }
  }

  /**
   * Refresh OAuth token using refresh token
   */
  async refreshToken(refreshToken: string): Promise<OAuthTokenResponse> {
    try {
      const response = await fetch(`${this.baseURL}/oauth/refresh`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${this.getAuthToken()}`,
        },
        body: JSON.stringify({ refresh_token: refreshToken }),
      })

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`)
      }

      const data = await response.json()

      if (!data.success || !data.data) {
        throw new Error('Invalid refresh response')
      }

      const token = data.data as OAuthTokenResponse

      // Update cache
      this.cacheToken('multi-audience', token)

      console.log('✅ Token refreshed successfully')
      return token

    } catch (error: any) {
      console.error('❌ Token refresh failed:', error)
      throw new Error(`Token refresh failed: ${error.message}`)
    }
  }

  /**
   * Proxy request to Floodgate with user identity
   * Automatically forwards X-Forwarded-For header with user's IP
   */
  async proxyToFloodgate<T = any>(request: FloodgateProxyRequest): Promise<T> {
    try {
      // Get multi-audience token
      const token = this.getCachedToken('multi-audience')
      if (!token) {
        throw new Error('No valid multi-audience token. Please authenticate first.')
      }

      // Check if token needs refresh
      if (this.isTokenExpiringSoon(token)) {
        if (token.token.refresh_token) {
          await this.refreshToken(token.token.refresh_token)
        } else {
          throw new Error('Token expiring and no refresh token available')
        }
      }

      // Build proxy URL
      const proxyURL = `${this.baseURL}/floodgate${request.path}`

      // Prepare headers
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token.token.access_token}`,
        ...request.headers,
      }

      // Prepare request options
      const options: RequestInit = {
        method: request.method,
        headers,
        credentials: 'include', // Include cookies
      }

      if (request.body && (request.method === 'POST' || request.method === 'PUT' || request.method === 'PATCH')) {
        options.body = JSON.stringify(request.body)
      }

      // Make request through AlertHub proxy
      // AlertHub backend will add X-Forwarded-For header with user's IP
      const response = await fetch(proxyURL, options)

      if (!response.ok) {
        const error = await response.json().catch(() => ({ message: response.statusText }))
        throw new Error(error.message || `HTTP ${response.status}`)
      }

      const data = await response.json()
      return data as T

    } catch (error: any) {
      console.error('❌ Floodgate proxy request failed:', error)
      throw new Error(`Floodgate request failed: ${error.message}`)
    }
  }

  /**
   * Get Floodgate models (most common use case)
   */
  async getFloodgateModels(): Promise<any[]> {
    return this.proxyToFloodgate<any>({
      method: 'GET',
      path: '/api/openai/v1/models',
    })
  }

  /**
   * Chat with Floodgate (OpenAI-compatible API)
   */
  async chatWithFloodgate(messages: any[], model?: string): Promise<any> {
    return this.proxyToFloodgate<any>({
      method: 'POST',
      path: '/api/openai/v1/chat/completions',
      body: {
        model: model || 'gpt-4',
        messages,
      },
    })
  }

  // ============================================================================
  // TOKEN CACHING
  // ============================================================================

  private cacheToken(key: string, token: OAuthTokenResponse) {
    const expiresAt = Date.now() + (token.expires_in * 1000)
    this.tokenCache.set(key, { token, expiresAt })

    // Also store in localStorage for persistence
    localStorage.setItem(`oauth_token_${key}`, JSON.stringify({ token, expiresAt }))
  }

  private getCachedToken(key: string): { token: OAuthTokenResponse; expiresAt: number } | null {
    // Check memory cache first
    let cached = this.tokenCache.get(key)

    // Fall back to localStorage
    if (!cached) {
      const stored = localStorage.getItem(`oauth_token_${key}`)
      if (stored) {
        try {
          cached = JSON.parse(stored)
          if (cached) {
            this.tokenCache.set(key, cached)
          }
        } catch (e) {
          console.error('Failed to parse cached token:', e)
        }
      }
    }

    // Check if token is still valid
    if (cached && cached.expiresAt > Date.now()) {
      return cached
    }

    // Token expired or doesn't exist
    this.clearCachedToken(key)
    return null
  }

  private isTokenExpiringSoon(cached: { token: OAuthTokenResponse; expiresAt: number }): boolean {
    const fiveMinutes = 5 * 60 * 1000
    return (cached.expiresAt - Date.now()) < fiveMinutes
  }

  private clearCachedToken(key: string) {
    this.tokenCache.delete(key)
    localStorage.removeItem(`oauth_token_${key}`)
  }

  /**
   * Clear all cached tokens
   */
  clearAllTokens() {
    this.tokenCache.clear()
    // Clear from localStorage
    Object.keys(localStorage).forEach(key => {
      if (key.startsWith('oauth_token_')) {
        localStorage.removeItem(key)
      }
    })
  }

  // ============================================================================
  // UTILITIES
  // ============================================================================

  private getAuthToken(): string {
    return sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''
  }

  /**
   * Check if multi-audience token is available and valid
   */
  hasValidMultiAudienceToken(): boolean {
    const cached = this.getCachedToken('multi-audience')
    return cached !== null
  }

  /**
   * Get current multi-audience token (for debugging)
   */
  getCurrentToken(): OAuthTokenResponse | null {
    const cached = this.getCachedToken('multi-audience')
    return cached?.token || null
  }
}

// Export singleton instance
export const corporateOAuthService = new CorporateOAuthService()

// Export types
export type { OAuthTokenResponse, FloodgateProxyRequest }
