

export function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>(complete => { resolve = complete; });
  return { promise, resolve };
}
