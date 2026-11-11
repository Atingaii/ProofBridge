import { useEffect, useRef, useState } from 'react';
import { Check, ChevronLeft, ChevronRight, HelpCircle, Send } from 'lucide-react';
import type { UserInputAnswer, UserInputItem, UserInputQuestion } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { cn, orchestrationParticipantLabel } from '../lib/utils';
import { Button } from './ui';

export function UserInputCard({ item, language, t, onSubmit, hasNextRequest = false }: {
  item: UserInputItem;
  language: 'zh' | 'en';
  t: UIText;
  onSubmit: (requestId: string, answers: Record<string, UserInputAnswer>) => void;
  hasNextRequest?: boolean;
}) {
  const [selected, setSelected] = useState<Record<string, string[]>>({});
  const [custom, setCustom] = useState<Record<string, string>>({});
  const [customActive, setCustomActive] = useState<Record<string, boolean>>({});
  const [questionIndex, setQuestionIndex] = useState(0);
  const customInputs = useRef<Record<string, HTMLInputElement | HTMLTextAreaElement | null>>({});
  const questionHeading = useRef<HTMLLegendElement>(null);
  useEffect(() => {
    if (item.status !== 'delivered') return;
    setSelected({});
    setCustom({});
    setCustomActive({});
  }, [item.status]);
  const zh = language === 'zh';
  const pending = item.status === 'pending';
  const questions = item.request.questions;
  const activeIndex = Math.min(questionIndex, Math.max(0, questions.length - 1));
  const hasCustom = (question: UserInputQuestion) => !(question.options || []).length || Boolean(customActive[question.id]);
  const isAnswered = (question: UserInputQuestion) => {
    // Explicitly choosing custom requires text, including for multi-select.
    if (hasCustom(question)) return Boolean(custom[question.id]?.trim());
    return (selected[question.id] || []).length > 0;
  };
  const answeredCount = questions.filter(isAnswered).length;
  const canSubmit = pending && questions.length > 0 && answeredCount === questions.length;
  const submit = () => {
    if (!canSubmit) return;
    const answers: Record<string, UserInputAnswer> = {};
    for (const question of questions) {
      const optionIds = selected[question.id] || [];
      const customText = hasCustom(question) ? custom[question.id] : undefined;
      answers[question.id] = { optionIds, ...(customText?.trim() ? { customText } : {}) };
    }
    onSubmit(item.request.requestId, answers);
  };
  const goTo = (index: number) => {
    setQuestionIndex(index);
    window.requestAnimationFrame(() => {
      questionHeading.current?.focus({ preventScroll: true });
      const heading = questionHeading.current;
      const panel = heading?.closest('#native-user-input-panel');
      if (heading && panel) {
        const offset = heading.getBoundingClientRect().top - panel.getBoundingClientRect().top;
        if (offset < 0 || offset > panel.clientHeight - 80) panel.scrollTop += offset - 12;
      }
    });
  };
  const choose = (question: UserInputQuestion, id: string) => {
    setSelected((current) => {
      const old = current[question.id] || [];
      return { ...current, [question.id]: question.selectionMode === 'multiple'
        ? old.includes(id) ? old.filter((value) => value !== id) : [...old, id]
        : [id] };
    });
    if (question.selectionMode !== 'multiple') setCustomActive((current) => ({ ...current, [question.id]: false }));
  };
  const editCustom = (question: UserInputQuestion, value: string) => {
    setCustom((current) => ({ ...current, [question.id]: value }));
    setCustomActive((current) => ({ ...current, [question.id]: true }));
    if (question.selectionMode !== 'multiple') setSelected((current) => ({ ...current, [question.id]: [] }));
  };
  if (item.status === 'delivered') return (
    <section id={`native-user-input-${item.request.requestId}`} tabIndex={-1}
      className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-border bg-card px-3 py-3 outline-none focus-visible:ring-2 focus-visible:ring-ring"
      aria-label={zh ? '原生用户问题' : 'Native user question'}>
      <h3 className="text-xs font-medium">{orchestrationParticipantLabel(item.request.cli, item.request.workerId)}</h3>
      <p role="status" className="flex items-center gap-1.5 text-xs text-emerald-700 dark:text-emerald-400"><Check aria-hidden="true" className="h-3.5 w-3.5" />{zh ? '答案已送达 CLI' : 'Answer delivered to CLI'}</p>
    </section>
  );
  return (
    <section id={`native-user-input-${item.request.requestId}`} tabIndex={-1} className="rounded-xl border border-border bg-card p-4 shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-primary md:p-5" aria-label={zh ? '原生用户问题' : 'Native user question'}>
      <div className="mb-3 flex items-start gap-2">
        <HelpCircle className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
        <div className="min-w-0">
          <h3 className="break-words text-sm font-semibold">{orchestrationParticipantLabel(item.request.cli, item.request.workerId)}</h3>
          <p className="text-xs text-muted-foreground">{item.status === 'submitting'
            ? zh ? '答案正在送达' : 'Answer is being delivered'
            : item.status === 'confirming' ? t.nativeConfirmingDelivery
            : item.request.blocking
            ? zh ? '正在等待你的回答' : 'Waiting for your answer'
            : zh ? '可回答；任务仍可继续' : 'You may answer while the task continues'}</p>
        </div>
      </div>
      {questions.length > 1 && <div className="mb-4 space-y-2">
        <div className="flex gap-1.5 overflow-x-auto pb-1" role="group" aria-label={zh ? '切换问题' : 'Switch questions'}>
          {questions.map((question, index) => <button key={question.id} type="button"
            aria-current={index === activeIndex ? 'step' : undefined}
            aria-label={zh ? `第 ${index + 1} 题${isAnswered(question) ? '，已填写' : '，未填写'}` : `Question ${index + 1}${isAnswered(question) ? ', answered' : ', unanswered'}`}
            onClick={() => goTo(index)}
            className={cn('flex min-h-9 shrink-0 items-center gap-1 rounded-md border px-3 text-xs font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary',
              isAnswered(question) ? 'border-emerald-500 bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200' : 'border-border text-muted-foreground hover:bg-muted/40',
              index === activeIndex && 'ring-2 ring-primary ring-offset-2 ring-offset-card')}>
            {isAnswered(question) && <Check className="h-3 w-3" aria-hidden="true" />}{index + 1} · {isAnswered(question) ? zh ? '已填写' : 'Answered' : zh ? '待填写' : 'Unanswered'}
          </button>)}
        </div>
        <p className="text-xs text-muted-foreground" role="status">{zh
          ? `已填写 ${answeredCount}/${questions.length} 题；可返回修改，最后统一提交。`
          : `${answeredCount}/${questions.length} answered. Review any question before submitting together.`}</p>
      </div>}
      {questions.map((question, index) => {
        if (index !== activeIndex) return null;
        const choices = selected[question.id] || [];
        const options = question.options || [];
        const isCustom = hasCustom(question);
        const multiple = question.selectionMode === 'multiple';
        const hintId = `question-hint-${item.request.requestId}-${index}`;
        const fieldClass = 'mt-2 w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-primary/50 disabled:opacity-60';
        return <fieldset key={question.id} className="min-w-0 space-y-2" disabled={!pending} aria-describedby={hintId}>
          <legend ref={questionHeading} tabIndex={-1} className="mb-1 w-full break-words text-sm font-medium leading-6 outline-none focus-visible:ring-2 focus-visible:ring-primary/50">
            {question.header && <span className="mb-1 block text-xs font-normal text-muted-foreground">{question.header}</span>}{question.question}
          </legend>
          <p className={cn('flex items-center gap-1 text-xs font-medium', isAnswered(question)
            ? 'text-emerald-700 dark:text-emerald-300' : 'text-muted-foreground')}>
            {isAnswered(question) && <Check className="h-3.5 w-3.5" aria-hidden="true" />}
            {isAnswered(question) ? zh ? '已填写，可继续修改' : 'Answered — you can still edit' : zh ? '待填写' : 'Unanswered'}
          </p>
          <p id={hintId} className="text-xs text-muted-foreground">{options.length
            ? multiple ? question.allowCustom ? zh ? '可多选，也可补充自己的回答' : 'Select multiple options or add your own answer'
                : zh ? '可选择多个选项' : 'Select one or more options'
              : question.allowCustom ? zh ? '选择一项，或输入自己的回答' : 'Select one option or type your own answer'
                : zh ? '请选择一项' : 'Select one option'
            : zh ? '请在下方输入回答' : 'Type your answer below'}</p>
          {options.map((option) => <label key={option.id} className={cn('flex min-h-11 items-start gap-3 rounded-lg border p-3 text-sm transition-colors focus-within:ring-2 focus-within:ring-primary/50',
            pending ? 'cursor-pointer hover:bg-muted/40' : 'cursor-default opacity-70',
            choices.includes(option.id) ? 'border-primary bg-primary/15 ring-1 ring-primary' : 'border-border')}>
            <input type={multiple ? 'checkbox' : 'radio'} name={`question-${item.request.requestId}-${index}`}
              checked={choices.includes(option.id)} onChange={() => choose(question, option.id)} className="mt-1 shrink-0 accent-primary" />
            <span className="min-w-0 break-words"><span className="font-medium">{option.label}</span>
              {option.description && <span className="mt-0.5 block text-xs leading-5 text-muted-foreground">{option.description}</span>}
            </span>
          </label>)}
          {(question.allowCustom || !options.length) && <div className={cn('rounded-lg border p-3 focus-within:ring-2 focus-within:ring-primary/50', isCustom ? 'border-primary bg-primary/15 ring-1 ring-primary' : 'border-border')}>
            <label className={cn('flex min-h-5 items-center gap-3 text-sm font-medium', pending && 'cursor-pointer')}>
              {options.length > 0 && <input type={multiple ? 'checkbox' : 'radio'} name={`question-${item.request.requestId}-${index}`}
                checked={isCustom} onChange={() => {
                  const next = multiple ? !isCustom : true;
                  setCustomActive((current) => ({ ...current, [question.id]: next }));
                  if (next && !multiple) setSelected((current) => ({ ...current, [question.id]: [] }));
                  if (next) customInputs.current[question.id]?.focus();
                }} className="shrink-0 accent-primary" />}
              {zh ? '自定义回答' : 'Custom answer'}
            </label>
            {question.secret ? <input type="password" autoComplete="off"
              ref={(element) => { customInputs.current[question.id] = element; }}
              aria-label={`${question.question} — ${zh ? '自定义回答' : 'Custom answer'}`}
              placeholder={zh ? '输入你的回答…' : 'Type something…'} value={custom[question.id] || ''}
              onChange={(event) => editCustom(question, event.target.value)} maxLength={8192} className={fieldClass} />
              : <textarea ref={(element) => { customInputs.current[question.id] = element; }}
                aria-label={`${question.question} — ${zh ? '自定义回答' : 'Custom answer'}`}
                placeholder={zh ? '输入你的回答…' : 'Type something…'} value={custom[question.id] || ''}
                onChange={(event) => editCustom(question, event.target.value)} maxLength={8192} rows={2}
                className={cn(fieldClass, 'resize-y')} />}
            {pending && isCustom && !custom[question.id]?.trim() && <p className="mt-1 text-xs text-muted-foreground">{zh ? '填写内容后才能提交。' : 'Enter an answer to continue.'}</p>}
          </div>}
        </fieldset>;
      })}
      {item.error && <p className="mt-3 text-xs text-destructive" role="alert">{item.error}</p>}
      {item.status !== 'pending' && <p className="mt-3 text-xs text-muted-foreground" role="status">{
        item.status === 'submitting' ? zh ? '正在送达…' : 'Sending…'
          : t.nativeConfirmingDelivery
      }</p>}
      <div className="sticky -bottom-3 mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-border bg-card py-3">
        {questions.length > 1 && <Button type="button" variant="ghost" size="sm" disabled={activeIndex === 0} onClick={() => goTo(activeIndex - 1)}>
          <ChevronLeft className="mr-1 h-3.5 w-3.5" />{zh ? '上一题' : 'Previous'}
        </Button>}
        {activeIndex < questions.length - 1 ? <Button type="button" size="sm" className="ml-auto" onClick={() => goTo(activeIndex + 1)}>
          {zh ? '下一题' : 'Next'}<ChevronRight className="ml-1 h-3.5 w-3.5" />
        </Button> : pending && <Button type="button" size="sm" className="ml-auto gap-1.5" onClick={submit} disabled={!canSubmit}>
          <Send className="h-3.5 w-3.5" />{hasNextRequest ? zh ? '提交并继续下一题' : 'Submit and continue' : zh ? '提交回答' : 'Submit answer'}
        </Button>}
      </div>
    </section>
  );
}
