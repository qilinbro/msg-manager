import type { KeyboardEvent } from 'react';
import type { ConversationKind } from './inbox-api';

const KINDS: { value: ConversationKind; label: string }[] = [
  { value: 'private', label: '私聊' },
  { value: 'group', label: '群聊' },
];

/** A shared, keyboard-accessible two-tab filter. The caller owns the tab panel. */
export function ConversationKindTabs({
  id,
  kind,
  onChange,
  disabled = false,
  label,
}: {
  id: string;
  kind: ConversationKind;
  onChange: (value: ConversationKind) => void;
  disabled?: boolean;
  label: string;
}) {
  const move = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let next: number;
    switch (event.key) {
      case 'ArrowRight':
      case 'ArrowLeft':
        next = (index + 1) % KINDS.length;
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = KINDS.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    onChange(KINDS[next].value);
    event.currentTarget.parentElement
      ?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
      [next]?.focus();
  };
  return (
    <div className="conversation-kind-tabs" role="tablist" aria-label={label}>
      {KINDS.map((item, index) => (
        <button
          type="button"
          role="tab"
          id={`${id}-${item.value}`}
          key={item.value}
          aria-controls={`${id}-panel`}
          aria-selected={kind === item.value}
          tabIndex={kind === item.value ? 0 : -1}
          disabled={disabled}
          onClick={() => onChange(item.value)}
          onKeyDown={(event) => move(event, index)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
