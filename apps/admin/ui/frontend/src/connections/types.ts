export interface Connection {
  id: string;
  name: string;
  url: string;
}

export interface Credentials {
  username: string;
  password: string;
}

export type ConnectionStatus = 'unchecked' | 'checking' | 'ready' | 'authentication' | 'degraded' | 'unreachable';

export type HealthState = 'unknown' | 'ok' | 'unavailable';

export interface ConnectionCheck {
  status: ConnectionStatus;
  api: HealthState;
  database: HealthState;
  checkedAt?: number;
}
