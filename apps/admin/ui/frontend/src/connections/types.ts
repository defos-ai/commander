export interface Connection {
  id: string;
  name: string;
  url: string;
}

export interface Credentials {
  username: string;
  password: string;
}

export interface Session {
  url: string;
  username: string;
}

export interface Deployment {
  runtime: 'native' | 'docker' | 'docker-compose';
  application: 'commander-api' | 'commander';
}

export type ConnectionStatus = 'unchecked' | 'checking' | 'ready' | 'authentication' | 'degraded' | 'unreachable';

export type HealthState = 'unknown' | 'ok' | 'unavailable';

export interface ConnectionCheck {
  status: ConnectionStatus;
  api: HealthState;
  database: HealthState;
  checkedAt?: number;
}
