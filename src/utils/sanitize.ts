import DOMPurify from 'dompurify';
import { marked } from 'marked';

export function sanitizeHtml(html: string): string {
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
  });
}

export async function renderMarkdown(markdown: string): Promise<string> {
  return sanitizeHtml(await marked.parse(markdown));
}

export function renderMarkdownSync(markdown: string): string {
  return sanitizeHtml(marked.parse(markdown, { async: false }));
}

export function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (char) => {
    switch (char) {
      case '&':
        return '&amp;';
      case '<':
        return '&lt;';
      case '>':
        return '&gt;';
      case '"':
        return '&quot;';
      case "'":
        return '&#39;';
      default:
        return char;
    }
  });
}
