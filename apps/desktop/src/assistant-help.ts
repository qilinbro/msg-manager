// UI-only handoff. Do not add chat bodies, business drafts, errors, credentials,
// implicit selections or an API `context` field to this request.
export type AssistantHelpRequest = { source: string; prompt: string };

// Match the service's decoded UTF-8 message limit, not JavaScript UTF-16 units.
export const ASSISTANT_MESSAGE_BYTE_LIMIT = 8000;
export function inspectAssistantMessage(message: string): {
  bytes: number;
  issue: 'nul' | 'too_long' | null;
} {
  const bytes = new TextEncoder().encode(message).byteLength;
  return {
    bytes,
    issue: message.includes('\0')
      ? 'nul'
      : bytes > ASSISTANT_MESSAGE_BYTE_LIMIT
        ? 'too_long'
        : null,
  };
}

export function normalizeAssistantHelpRequest(
  request: AssistantHelpRequest,
): AssistantHelpRequest | null {
  if (
    !request ||
    typeof request.source !== 'string' ||
    typeof request.prompt !== 'string'
  )
    return null;
  const source = request.source.trim();
  const prompt = request.prompt.trim();
  // Reject rather than silently truncate the task the user is asked to review.
  if (!source || source.length > 120 || !prompt || prompt.length > 6000)
    return null;
  return { source, prompt };
}

export type AssistantHelpDraftResult =
  | { ok: true; draft: string }
  | {
      ok: false;
      reason: 'invalid' | 'unresolved' | 'nonempty' | 'too_long' | 'nul';
    };

export function prepareAssistantHelpDraft(
  draft: string,
  request: AssistantHelpRequest,
  mode: 'fill' | 'append',
  hasUnresolved: boolean,
): AssistantHelpDraftResult {
  if (hasUnresolved) return { ok: false, reason: 'unresolved' };
  const help = normalizeAssistantHelpRequest(request);
  if (!help) return { ok: false, reason: 'invalid' };
  if (draft.length && mode !== 'append')
    return { ok: false, reason: 'nonempty' };
  const task = `来源：${help.source}\n${help.prompt}`;
  const next = draft.length ? `${draft}\n\n${task}` : task;
  const { issue } = inspectAssistantMessage(next);
  if (issue) return { ok: false, reason: issue };
  return { ok: true, draft: next };
}
