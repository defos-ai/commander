import { useEffect, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { useTranslation } from '../i18n';
import { Button } from './Button';

// Add whitespace between validated JSON tokens without reserializing numbers
// or string escapes from the saved definition.
function formatJSON(source: string): string {
  const tokens = source.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:]+/g) ?? [];
  let depth = 0;
  let result = '';
  const newline = () => '\n' + '  '.repeat(depth);
  for (const [index, token] of tokens.entries()) {
    if (token === '{' || token === '[') {
      result += token;
      if (tokens[index + 1] !== (token === '{' ? '}' : ']')) {
        depth++;
        result += newline();
      }
    } else if (token === '}' || token === ']') {
      if (tokens[index - 1] !== (token === '}' ? '{' : '[')) {
        depth--;
        result += newline();
      }
      result += token;
    } else if (token === ',') result += ',' + newline();
    else if (token === ':') result += ': ';
    else result += token;
  }
  return result + '\n';
}

export function MissionJSON({ definition }: { definition: string }) {
  const { t } = useTranslation();
  const text = formatJSON(definition);
  const [copy, setCopy] = useState<'copying' | 'copied' | 'failed'>();
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  async function copyDefinition() {
    setCopy('copying');
    try {
      await navigator.clipboard.writeText(text);
      if (mounted.current) setCopy('copied');
    } catch {
      if (mounted.current) setCopy('failed');
    }
  }

  return <div className="mission-json">
    <div className="mission-json-toolbar">
      {copy === 'failed' && <p className="form-hint" role="status">{t($ => $.missions.copyFailed)}</p>}
      <Button variant="secondary" onClick={() => void copyDefinition()} disabled={copy === 'copying'}>
        {copy === 'copied' ? <Check size={15} aria-hidden="true" /> : <Copy size={15} aria-hidden="true" />}
        {t($ => copy === 'copied' ? $.missions.copied : $.missions.copyJSON)}
      </Button>
    </div>
    <textarea aria-label={t($ => $.missions.definition)} value={text} readOnly rows={24}
      spellCheck={false} autoCapitalize="none" />
  </div>;
}
