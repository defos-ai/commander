import { useEffect, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { getDeployment, normalizeURL } from '../connections/client';
import type { Deployment } from '../connections/types';
import { useTranslation } from '../i18n';

const commands = {
  'commander-api': {
    native: './commander-api credentials',
    docker: 'docker exec commander-api commander-api credentials',
    'docker-compose': 'docker compose exec api commander-api credentials',
  },
  commander: {
    native: '/credentials',
    docker: 'docker exec commander commander credentials api',
    'docker-compose': null,
  },
} as const;

export function CredentialHint({ url }: { url: string }) {
  const { t } = useTranslation();
  const [result, setResult] = useState<{ url: string; deployment: Deployment }>();
  const [copy, setCopy] = useState<{ url: string; status: 'copied' | 'failed' }>();

  useEffect(() => {
    let base: string;
    try { base = normalizeURL(url); } catch { return; }
    const controller = new AbortController();
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const debounce = setTimeout(() => {
      timeout = setTimeout(() => controller.abort(), 5_000);
      void getDeployment(base, controller.signal).then(deployment => {
        if (deployment && !controller.signal.aborted) setResult({ url, deployment });
      }).finally(() => clearTimeout(timeout));
    }, 300);
    return () => {
      clearTimeout(debounce);
      clearTimeout(timeout);
      controller.abort();
    };
  }, [url]);

  const deployment = result?.url === url ? result.deployment : undefined;
  const command = deployment && commands[deployment.application][deployment.runtime];
  if (!command) return null;
  const copied = copy?.url === url && copy.status === 'copied';
  const failed = copy?.url === url && copy.status === 'failed';

  async function copyCommand(value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopy({ url, status: 'copied' });
    } catch {
      setCopy({ url, status: 'failed' });
    }
  }

  return <div className="credential-hint">
    <p className="form-hint">{t($ => command === '/credentials' ? $.connectionForm.needPasswordInPlayground : $.connectionForm.needPassword)}</p>
    <div className="credential-command">
      <code>{command}</code>
      <button type="button" className="icon-button" aria-label={t($ => copied ? $.connectionForm.copied : $.connectionForm.copyCommand)}
        title={t($ => copied ? $.connectionForm.copied : $.connectionForm.copyCommand)} onClick={() => void copyCommand(command)}>
        {copied ? <Check size={16} aria-hidden="true" /> : <Copy size={16} aria-hidden="true" />}
      </button>
    </div>
    {failed && <p className="form-hint" role="status">{t($ => $.connectionForm.copyManually)}</p>}
  </div>;
}
