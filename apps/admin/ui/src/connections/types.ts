export interface Connection {
  id: string;
  name: string;
  url: string;
}

export type ConnectionStatus = 'unchecked' | 'checking' | 'ready' | 'authentication' | 'degraded' | 'unreachable';
