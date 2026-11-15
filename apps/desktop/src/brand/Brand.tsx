import { chineseWordmark, latinWordmark, relayPath } from './identity';

export function RelaySymbol() {
  return (
    <svg viewBox="0 0 64 64" fill="currentColor" aria-hidden="true">
      <path className="relay-in" d={relayPath} />
      <path className="relay-out" d={relayPath} transform="rotate(180 32 32)" />
    </svg>
  );
}

export function BrandLockup() {
  return (
    <svg
      viewBox="0 0 168 64"
      fill="currentColor"
      role="img"
      aria-label="接班 Away Desk"
    >
      <path d={relayPath} />
      <path className="relay-out" d={relayPath} transform="rotate(180 32 32)" />
      <path d={chineseWordmark} />
      <path className="brand-latin" d={latinWordmark} />
    </svg>
  );
}
