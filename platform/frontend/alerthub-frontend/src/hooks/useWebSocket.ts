import { useEffect, useRef, useState } from 'react';
import { useEnhancedAuthStore } from '@/stores/enhancedAuthStore';

interface UseWebSocketOptions {
  onMessage?: (data: any) => void;
  onConnect?: () => void;
  onDisconnect?: () => void;
  onError?: (error: Event) => void;
  reconnectInterval?: number;
  maxReconnectAttempts?: number;
  silenceTimeoutMs?: number;
}

export function useWebSocket(url: string, options: UseWebSocketOptions = {}) {
  const [isConnected, setIsConnected] = useState(false);
  // Display-only counter — NOT in the effect dep array to prevent reconnect storm.
  // The authoritative count lives in reconnectCountRef below.
  const [reconnectCount, setReconnectCount] = useState(0);
  const [lastMessage, setLastMessage] = useState<MessageEvent | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout>>();
  const heartbeatIntervalRef = useRef<ReturnType<typeof setInterval>>();
  const lastMessageAtRef = useRef<number>(Date.now());
  // Ref so onclose captures the live count without needing a state dep.
  const reconnectCountRef = useRef(0);
  // Ref so callbacks (onMessage, onConnect …) are always the latest version
  // without adding them to the effect dep array (which would reconnect on every render).
  const optionsRef = useRef(options);
  optionsRef.current = options;

  const tokens = useEnhancedAuthStore((state) => state.tokens);
  const access_token = tokens?.access_token || sessionStorage.getItem('access_token') || localStorage.getItem('access_token') || ''

  const {
    reconnectInterval = 5000,
    maxReconnectAttempts = 10,
    silenceTimeoutMs = 60_000,
  } = options;

  useEffect(() => {
    if (!access_token) return;

    reconnectCountRef.current = 0;
    setReconnectCount(0);

    const connect = () => {
      try {
        const wsUrl = url.replace(/^http/, 'ws');
        // NOTE: token in query string is a known limitation — preferred alternative
        // (first-frame auth message) requires backend coordination, deferred post-go-live.
        const urlWithToken = `${wsUrl}?token=${access_token}`;

        const ws = new WebSocket(urlWithToken);

        ws.onopen = () => {
          console.log('[WebSocket] Connected to', url);
          setIsConnected(true);
          reconnectCountRef.current = 0;
          setReconnectCount(0);
          lastMessageAtRef.current = Date.now();
          optionsRef.current.onConnect?.();

          if (heartbeatIntervalRef.current) clearInterval(heartbeatIntervalRef.current);
          heartbeatIntervalRef.current = setInterval(() => {
            if (ws.readyState !== WebSocket.OPEN) return;
            if (Date.now() - lastMessageAtRef.current > silenceTimeoutMs) {
              console.warn('[WebSocket] Silence timeout — forcing reconnect');
              ws.close(4000, 'Silence timeout');
            }
          }, Math.min(silenceTimeoutMs / 2, 30_000));
        };

        ws.onmessage = (event) => {
          lastMessageAtRef.current = Date.now();
          setLastMessage(event);
          try {
            const data = JSON.parse(event.data);
            optionsRef.current.onMessage?.(data);
          } catch {
            optionsRef.current.onMessage?.(event.data);
          }
        };

        ws.onclose = (event) => {
          console.log('[WebSocket] Disconnected:', event.code, event.reason);
          if (heartbeatIntervalRef.current) {
            clearInterval(heartbeatIntervalRef.current);
            heartbeatIntervalRef.current = undefined;
          }
          setIsConnected(false);
          optionsRef.current.onDisconnect?.();

          if (event.code !== 1000 && reconnectCountRef.current < maxReconnectAttempts) {
            const next = reconnectCountRef.current + 1;
            console.log(`[WebSocket] Reconnecting in ${reconnectInterval}ms... (Attempt ${next}/${maxReconnectAttempts})`);
            reconnectCountRef.current = next;
            setReconnectCount(next);
            reconnectTimeoutRef.current = setTimeout(connect, reconnectInterval);
          } else if (reconnectCountRef.current >= maxReconnectAttempts) {
            console.error('[WebSocket] Max reconnection attempts reached');
          }
        };

        ws.onerror = (error) => {
          console.error('[WebSocket] Error:', error);
          optionsRef.current.onError?.(error);
        };

        wsRef.current = ws;
      } catch (error) {
        console.error('[WebSocket] Connection failed:', error);
      }
    };

    connect();

    return () => {
      if (reconnectTimeoutRef.current) clearTimeout(reconnectTimeoutRef.current);
      if (heartbeatIntervalRef.current) clearInterval(heartbeatIntervalRef.current);
      const ws = wsRef.current;
      wsRef.current = null;
      if (ws) {
        ws.onmessage = null;
        ws.onerror = null;
        ws.onclose = null;
        if (ws.readyState === WebSocket.OPEN) {
          ws.close(1000, 'Component unmounted');
        } else if (ws.readyState === WebSocket.CONNECTING) {
          ws.onopen = () => ws.close(1000, 'Component unmounted');
        }
      }
    };
  }, [url, access_token, reconnectInterval, maxReconnectAttempts, silenceTimeoutMs]);

  const send = (data: any) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      try {
        const message = typeof data === 'string' ? data : JSON.stringify(data);
        wsRef.current.send(message);
        return true;
      } catch (error) {
        console.error('[WebSocket] Failed to send message:', error);
        return false;
      }
    } else {
      console.warn('[WebSocket] Cannot send message - connection not open');
      return false;
    }
  };

  const disconnect = () => {
    if (wsRef.current) {
      wsRef.current.close(1000, 'Manually disconnected');
    }
  };

  return {
    isConnected,
    send,
    disconnect,
    reconnectAttempts: reconnectCount,
    lastMessage,
  };
}
