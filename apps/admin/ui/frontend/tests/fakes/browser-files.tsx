import { Blob as NodeBlob } from 'node:buffer';
import { vi } from 'vitest';

export function browserFiles() {
  const blobs: Blob[] = [];
  const createObjectURL = vi.fn((blob: Blob) => { blobs.push(blob); return `blob:fixture-${blobs.length}`; });
  const revokeObjectURL = vi.fn();
  vi.stubGlobal('Blob', NodeBlob);
  vi.stubGlobal('URL', class extends URL {
    static createObjectURL = createObjectURL;
    static revokeObjectURL = revokeObjectURL;
  });
  const downloads: { href: string; name: string }[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { downloads.push({ href: this.href, name: this.download }); });
  return { get blobs() { return [...blobs]; }, get downloads() { return downloads.map(value => ({ ...value })); }, createObjectURL, revokeObjectURL };
}
