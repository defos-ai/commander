import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { afterAll, beforeAll, expect, it } from 'vitest';
import { Dialog } from '../src/components/Dialog';

beforeAll(() => {
  // jsdom does not implement the browser's native dialog lifecycle.
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, value(this: HTMLDialogElement) { this.open = true; } },
    close: { configurable: true, value(this: HTMLDialogElement) { this.open = false; } },
  });
});

afterAll(() => {
  Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
});

function Example() {
  const [open, setOpen] = useState(false);
  return <>
    <button onClick={() => setOpen(true)}>Add connection</button>
    <Dialog open={open} onClose={() => setOpen(false)} title="New connection" description="Connect to your Commander API.">
      <label>Connection name<input /></label>
    </Dialog>
  </>;
}

it.each(['cancel', 'close button'] as const)('closes via %s, restores focus and can reopen', method => {
  render(<Example />);
  const trigger = screen.getByRole('button', { name: 'Add connection' });
  const previousOverflow = document.body.style.overflow;
  trigger.focus();
  fireEvent.click(trigger);
  const dialog = screen.getByRole('dialog', { name: 'New connection' });
  screen.getByRole('textbox', { name: 'Connection name' }).focus();

  if (method === 'cancel') fireEvent(dialog, new Event('cancel', { cancelable: true }));
  else fireEvent.click(screen.getByRole('button', { name: 'Close dialog' }));

  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  expect(document.body.style.overflow).toBe(previousOverflow);

  fireEvent.click(trigger);
  expect(screen.getByRole('dialog', { name: 'New connection' })).toHaveAccessibleDescription('Connect to your Commander API.');
  expect(document.body.style.overflow).toBe('hidden');
});
