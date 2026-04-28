/**
 * Shared helper that watches for theme changes via both the
 * `data-theme` attribute on <html> and the `prefers-color-scheme` media query.
 *
 * Returns a cleanup function that removes all listeners.
 */
export function useThemeWatch(callback: () => void): () => void {
  const preferredDark = globalThis.matchMedia?.('(prefers-color-scheme: dark)');
  preferredDark?.addEventListener('change', callback);

  const observer = new MutationObserver(callback);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme'],
  });

  return () => {
    preferredDark?.removeEventListener('change', callback);
    observer.disconnect();
  };
}
