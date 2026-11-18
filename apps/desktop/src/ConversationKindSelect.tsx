import type { ConversationKind } from './inbox-api';

type KindSelection = ConversationKind | 'all';
type Props = {
  id: string;
  disabled?: boolean;
  label?: string;
} & (
  | {
      includeAll: true;
      value: KindSelection;
      onChange: (value: KindSelection) => void;
    }
  | {
      includeAll?: false;
      value: ConversationKind;
      onChange: (value: ConversationKind) => void;
    }
);

export function ConversationKindSelect(props: Props) {
  return (
    <select
      id={props.id}
      className="conversation-kind-select"
      aria-label={props.label ?? '会话类型'}
      value={props.value}
      disabled={props.disabled}
      onChange={(event) => {
        const value = event.target.value;
        if (value === 'private' || value === 'group') props.onChange(value);
        else if (value === 'all' && props.includeAll) props.onChange(value);
      }}
    >
      {props.includeAll ? <option value="all">全部类型</option> : null}
      <option value="private">私聊</option>
      <option value="group">群聊</option>
    </select>
  );
}
