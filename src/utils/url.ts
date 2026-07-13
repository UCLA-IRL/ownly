/**
 * Escape NDN name to URL parameter.
 * All / are replaced with -
 * All - are replaced with %2D
 */
export function escapeUrlName(input: string) {
  if (input[0] === '/') input = input.slice(1);
  return input.replace(/-/g, '--').replace(/\//g, '-');
}

/**
 * Remove the standard /ndn prefix from a workspace or user name for display.
 * The underlying stored or routed value should remain untouched.
 */
export function stripNdnPrefixForDisplay(input: string): string {
  if (!input) return input;

  const stripped = input.replace(/^\/ndn(?=\/|$)/, '');
  return stripped || '/';
}

/**
 * Restore the hidden /ndn prefix for workspace or user names that are entered
 * in display form.
 */
export function restoreNdnPrefixFromDisplay(input: string): string {
  if (!input) return input;
  if (input === '/ndn' || input.startsWith('/ndn/')) return input;
  if (input.startsWith('/')) return `/ndn${input === '/' ? '' : input}`;
  return input;
}

/**
 * Unescape URL parameter to NDN name.
 * @param name Escaped NDN name
 */
export function unescapeUrlName(input: string) {
  let output = input.replace(/--/g, '||').replace(/-/g, '/').replace(/\|\|/g, '-');
  if (output[0] !== '/') output = '/' + output;
  return output;
}

/**
 * Encode JSON data to Uint8Array
 * @param data Data to encode
 */
export function byteify<T>(data: T): Uint8Array {
  const encoder = new TextEncoder();
  return encoder.encode(JSON.stringify(data));
}

/**
 * Decode JSON data from Uint8Array
 * @param data Data to decode
 */
export function unbyteify<T>(data: Uint8Array): T {
  const decoder = new TextDecoder();
  return JSON.parse(decoder.decode(data));
}

/**
 * Normalize a path.
 * @param path Path to normalize
 */
export function normalizePath(path: string): string {
  if (!path) return path;
  if (path[0] !== '/') path = '/' + path;
  return path.replace(/\/+/g, '/');
}

/**
 * Convert uint8Array to hex string.
 *
 * @param u8 Uint8Array to convert
 * @returns Hex string
 */
export function toHex(u8: Uint8Array): string {
  return Array.from(u8)
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

/**
 * Convert hex string to uint8Array.
 *
 * @param hex Hex string to convert
 * @returns Uint8Array
 */
export function fromHex(hex: string): Uint8Array {
  if (hex.length == 0) return new Uint8Array()
  return new Uint8Array(hex.match(/.{1,2}/g)!.map((byte) => parseInt(byte, 16)));
}
