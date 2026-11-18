import type { ReactNode } from 'react';

// Match SQLite lower()/instr(): literal substrings, ASCII case folding only.
// Do not use a regular expression constructed from the user's keyword.
export const asciiFold = (value: string) =>
  value.replace(/[A-Z]/g, (character) => character.toLowerCase());

export const keywordError = (value: string): string | null => {
  const query = value.trim();
  if (query.includes('\u0000')) return '关键词不能包含空字符。';
  return new TextEncoder().encode(query).byteLength > 256
    ? '关键词最多256字节（约85个汉字），请缩短后再搜索。'
    : null;
};

export function KeywordMatch({
  text,
  keyword,
}: {
  text: string;
  keyword: string;
}) {
  const query = keyword.trim();
  if (!query || keywordError(query)) return <>{text}</>;
  const folded = asciiFold(text);
  const needle = asciiFold(query);
  const parts: ReactNode[] = [];
  let start = 0;
  // Bound DOM growth for long imported bodies; the remaining text stays intact.
  for (let count = 0; count < 100; count++) {
    const found = folded.indexOf(needle, start);
    if (found < 0) break;
    if (found > start) parts.push(text.slice(start, found));
    parts.push(
      <mark key={found}>{text.slice(found, found + query.length)}</mark>,
    );
    start = found + query.length;
  }
  parts.push(text.slice(start));
  return <>{parts}</>;
}
