import type { ReactNode } from 'react';

/** Optional guidance and receipts stay available without occupying the work area. */
export function HelpDetails({
  label,
  children,
  className = '',
  descriptions = [],
}: {
  label: string;
  children: ReactNode;
  className?: string;
  descriptions?: readonly { id: string; text: string }[];
}) {
  const disclosure = (
    <details className={`help-details ${className}`.trim()}>
      <summary>{label}</summary>
      <div className="help-details-body">{children}</div>
    </details>
  );
  if (!descriptions.length) return disclosure;

  // Closed native details can suppress computed descriptions even when IDREFs
  // resolve. Keep plain-text mirrors outside that subtree, never duplicate IDs.
  return (
    <div className="help-details-with-descriptions">
      {descriptions.map(({ id, text }) => (
        <span key={id} id={id} className="help-accessible-description">
          {text}
        </span>
      ))}
      {disclosure}
    </div>
  );
}
