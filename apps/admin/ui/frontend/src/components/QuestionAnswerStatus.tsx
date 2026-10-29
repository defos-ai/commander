import { useEffect, useState } from 'react';
import type { OperationQuestion } from '../api/operation-types';
import { useTranslation } from '../i18n';

export function QuestionAnswerStatus({ question, active }: { question: OperationQuestion; active: boolean }) {
  const { t } = useTranslation();
  const [now, setNow] = useState(Date.now);
  const fallback = Date.parse(question.fallback_at);

  useEffect(() => {
    if (!active || question.status !== 'pending' || !Number.isFinite(fallback)) return;
    let timer: number | undefined;
    function update() {
      const current = Date.now();
      setNow(current);
      if (current < fallback) timer = window.setTimeout(update, Math.min(1000, fallback - current));
    }
    update();
    return () => window.clearTimeout(timer);
  }, [active, fallback, question.status]);

  if (question.status === 'answered') {
    const origin = question.answered_by;
    const label = origin ? t($ => $.operations.answerOrigins[origin]) : t($ => $.operations.answerOriginUnknown);
    return <p className="form-hint"><span>{t($ => $.operations.answeredBy)}</span>: <strong>{label}</strong></p>;
  }
  if (!active || question.status !== 'pending' || !Number.isFinite(fallback)) return null;
  const seconds = Math.max(0, Math.ceil((fallback - now) / 1000));
  if (seconds === 0) return <p className="form-hint" role="status">{t($ => $.operations.modelAnswerAvailable)}</p>;
  const minutes = Math.floor(seconds / 60);
  const time = `${minutes}:${String(seconds % 60).padStart(2, '0')}`;
  return <p className="form-hint" role="timer" aria-live="off">{t($ => $.operations.modelAnswerWaiting, { time })}</p>;
}
