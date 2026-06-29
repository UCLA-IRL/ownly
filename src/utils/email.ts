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

  const parts = email.toLowerCase().split('@');
  const user = parts[0];
  const domain = parts[1];

  // Technically @ is not allowed in URI, but NDNd can handle this
  return `/${domain}/@${user}`;
}

export function convertEmailToNameLegacy(email: string): string {
  // Legacy implementation, remove when NDNCERT is updated
  const parts = email.toLowerCase().split('@');
  const domain = parts[1].split('.').reverse();
  return '/' + ['ndn', ...domain, parts[0]].join('/');
}

export function convertDomainToName(domain: string): string {
  // Mirrors the Go-side NDNCERT DNS challenge: the issued testbed cert's
  // identity name is the testbed CA prefix with the verified domain
  // appended as a single component. For the default `/ndn` testbed the
  // resulting identity is `/ndn/<domain>`.
  return '/ndn/' + domain.toLowerCase();
}
