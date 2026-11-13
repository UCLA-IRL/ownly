import nodemailer from 'nodemailer';
import { marked } from 'marked';
import sanitizeHtml from 'sanitize-html';

export type MailConfig = {
  from: string;
  to: string[];
  subject: string;
  smtp: {
    host: string;
    port: number;
    security: 'tls' | 'starttls' | 'plain';
    user?: string;
    pass?: string;
  };
};

export function validateMail(config: MailConfig): void {
  const address = /^[^\s<>@,;]+@[^\s<>@,;]+\.[^\s<>@,;]+$/;
  if (
    !address.test(config.from) ||
    !config.to.length ||
    config.to.some((to) => !address.test(to))
  ) {
    throw new Error('Invalid EMAIL_FROM or EMAIL_TO; use bare email addresses');
  }
  if (new Set(config.to.map((to) => to.toLowerCase())).size !== config.to.length) {
    throw new Error('Invalid EMAIL_TO: duplicate recipients');
  }
  if (!config.subject.trim() || /[\r\n]/.test(config.subject)) {
    throw new Error('Invalid EMAIL_SUBJECT');
  }
  const smtp = config.smtp;
  if (!smtp.host.trim() || /\s/.test(smtp.host)) {
    throw new Error('Invalid SMTP_HOST');
  }
  if (!Number.isInteger(smtp.port) || smtp.port < 1 || smtp.port > 65535) {
    throw new Error('Invalid SMTP_PORT');
  }
  if (!['tls', 'starttls', 'plain'].includes(smtp.security)) {
    throw new Error('Invalid SMTP_SECURITY');
  }
  if (smtp.security === 'plain' && !['localhost', '127.0.0.1', '::1'].includes(smtp.host)) {
    throw new Error('SMTP plaintext is restricted to loopback testing');
  }
  if (Boolean(smtp.user) !== Boolean(smtp.pass)) {
    throw new Error('SMTP_USER and SMTP_PASS are both required for authentication');
  }
}

/** No retry: a lost SMTP acknowledgement can mean delivery already happened. */
export async function sendAgenda(markdown: string, config: MailConfig) {
  validateMail(config);
  const smtp = config.smtp;
  const transport = nodemailer.createTransport({
    host: smtp.host,
    port: smtp.port,
    secure: smtp.security === 'tls',
    requireTLS: smtp.security === 'starttls',
    ignoreTLS: smtp.security === 'plain',
    auth: smtp.user ? { user: smtp.user, pass: smtp.pass } : undefined,
  });
  try {
    const info = await transport.sendMail({
      from: config.from,
      to: config.to,
      subject: config.subject,
      text: markdown,
      html: renderAgenda(markdown),
      disableFileAccess: true,
      disableUrlAccess: true,
    });
    if (info.rejected.length || info.accepted.length !== config.to.length) {
      throw new Error('not all recipients accepted');
    }
    return { status: 'sent' as const, messageId: info.messageId };
  } catch {
    // SMTP responses can contain private addresses/authentication diagnostics.
    throw new Error('SMTP delivery failed or acceptance uncertain; do not blindly retry');
  } finally {
    transport.close();
  }
}

export function renderAgenda(markdown: string): string {
  const content = sanitizeHtml(marked.parse(markdown, { async: false }));
  return `<!doctype html><html><head><meta charset="utf-8"></head><body style="font-family:Arial,sans-serif;line-height:1.5;max-width:800px;margin:auto;padding:24px"><p>This is a reminder of the upcoming NDN call using Zoom <a href="https://arizona.zoom.us/j/82909523174?pwd=bEc2VXk4M3ZrdC95SGlxUHVzRVFyUT09">https://arizona.zoom.us/j/82909523174?pwd=bEc2VXk4M3ZrdC95SGlxUHVzRVFyUT09</a><br>The current call time is every Friday 09:00-11:00 Pacific Time.</p><hr><main>${content}</main></body></html>`;
}

export function calendarDate(now: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now);
}

function parseDay(value: string): Date {
  const day = new Date(`${value}T00:00:00Z`);
  if (
    !/^\d{4}-\d{2}-\d{2}$/.test(value) ||
    !Number.isFinite(+day) ||
    day.toISOString().slice(0, 10) !== value
  ) {
    throw new Error('Invalid agenda calendar date');
  }
  return day;
}

/** Calendar headings are dates, not UTC instants. Weeks start on Monday. */
export function selectAgenda(markdown: string, today: string) {
  const day = parseDay(today);
  day.setUTCDate(day.getUTCDate() - ((day.getUTCDay() + 6) % 7) - 7);
  const cutoff = day.toISOString().slice(0, 10);
  markdown = markdown.replace(/\r\n?|\n/g, '\n');
  let offset = 0;
  const headings: { date: string; index: number }[] = [];
  for (const token of marked.lexer(markdown)) {
    const index = markdown.indexOf(token.raw, offset);
    offset = index + token.raw.length;
    const date =
      token.type === 'heading' && token.depth === 2
        ? token.text.match(/^(\d{4}-\d{2}-\d{2})\b/)?.[1]
        : undefined;
    if (date) {
      parseDay(date);
      headings.push({ date, index });
    }
  }
  const sections = headings.map((heading, i) => ({
    date: heading.date,
    text: markdown.slice(heading.index, headings[i + 1]?.index ?? markdown.length).trimEnd(),
  }));
  let selected = sections.filter((s) => s.date >= cutoff);
  const staleFallback = sections.length > 0 && selected.length === 0;
  if (staleFallback) {
    const newest = [...sections].sort((a, b) => b.date.localeCompare(a.date)).slice(0, 2);
    selected = sections.filter((s) => newest.includes(s));
  }
  return {
    markdown: sections.length
      ? markdown.slice(0, headings[0]!.index) + selected.map((s) => s.text).join('\n\n') + '\n'
      : markdown,
    dates: selected.map((s) => s.date),
    staleFallback,
  };
}
