export function upstreamURL(input: RequestInfo | URL): string {
  const value = String(input);
  if (!value.startsWith('/ui/proxy?')) return value;
  const query = new URL(value, 'http://ui.test').searchParams;
  return `${query.get('url')}${query.get('path')}`;
}
