import { useCallback, useEffect, useRef } from 'react';

function safeFilename(name: string) {
  return name.replace(/[<>:"/\\|?*\u0000-\u001f\u007f]/g, '_').replace(/^\.+/, '').replace(/[. ]+$/, '').slice(0, 200) || 'download';
}

export function useBlobDownload() {
  const urls = useRef(new Set<string>());
  useEffect(() => () => { for (const url of urls.current) URL.revokeObjectURL(url); urls.current.clear(); }, []);
  return useCallback((blob: Blob, filename: string) => {
    const url = URL.createObjectURL(blob);
    urls.current.add(url);
    const link = document.createElement('a');
    link.href = url;
    link.download = safeFilename(filename);
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => { if (urls.current.delete(url)) URL.revokeObjectURL(url); }, 1000);
  }, []);
}
