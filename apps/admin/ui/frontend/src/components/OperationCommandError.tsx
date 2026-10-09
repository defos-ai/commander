import type { CommandError } from '../api/operation-commands';
import { useTranslation } from '../i18n';
import messages from '../i18n/locales/en/operationCommands';
import { Button } from './Button';

export function OperationCommandError({ error, operationID, onInspect }: {
  error: CommandError;
  operationID?: string;
  onInspect?: () => void;
}) {
  const { t } = useTranslation();
  const code = error.code;
  const message = code && Object.hasOwn(messages.commandErrors, code)
    ? t($ => $.operationCommands.commandErrors[code as keyof typeof messages.commandErrors])
    : t($ => $.operationCommands.errorKinds[error.kind]);
  return <div className="operation-command-error" role="alert">
    <p>{message}</p>
    {operationID && <p className="operation-command-identity">{t($ => $.operationCommands.operationID)}: <code>{operationID}</code></p>}
    {onInspect && <Button variant="secondary" onClick={onInspect}>{t($ => $.operationCommands.viewOperation)}</Button>}
  </div>;
}
