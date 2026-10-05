import { useEffect, useId, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { useTranslation } from '../i18n';

export function Dialog({ open, title, description, onClose, children }: {
  open: boolean;
  title: string;
  description?: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const descriptionID = useId();
  const { t } = useTranslation();

  useEffect(() => {
    const dialog = ref.current;
    if (!open || !dialog) return;
    const previousFocus = document.activeElement;
    const overflow = document.body.style.overflow;
    dialog.showModal();
    document.body.style.overflow = 'hidden';
    return () => {
      dialog.close();
      document.body.style.overflow = overflow;
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
    };
  }, [open]);

  return (
    <dialog ref={ref} className="dialog" aria-labelledby={titleID} aria-describedby={description ? descriptionID : undefined}
      onCancel={event => { event.preventDefault(); onClose(); }}>
      <div className="dialog-header">
        <h2 id={titleID}>{title}</h2>
        <button type="button" className="icon-button" aria-label={t($ => $.dialog.close)} onClick={onClose}><X size={20} aria-hidden="true" /></button>
      </div>
      {description && <p className="dialog-description" id={descriptionID}>{description}</p>}
      {children}
    </dialog>
  );
}
