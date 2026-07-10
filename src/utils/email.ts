/**
 * Validate email address
 * @param email Email to validate
 * @returns True if email is valid, false otherwise
 */
export function validateEmail(email: string): boolean {
  return !!email
    .toLowerCase()
    .match(
      /^(([^<>()[\]\\.,;:\s@"]+(\.[^<>()[\]\\.,;:\s@"]+)*)|.(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$/,
    );
}

export function convertEmailToName(email: string): string {
  if (!validateEmail(email)) {
    throw new Error('Invalid email address');
  }

  return '/ndn/' + encodeURIComponent(email.toLowerCase());
}

export function convertEmailToNameLegacy(email: string): string {
  // Legacy implementation, remove when NDNCERT is updated
  const parts = email.toLowerCase().split('@');
  const domain = parts[1].split('.').reverse();
  return '/' + ['ndn', ...domain, parts[0]].join('/');
}
