import { describe, expect, it, vi } from 'vitest';

import { toast } from '../../primitives';
import type { PrinterTransferRow } from './status';
import { TransferAnnouncer } from './transfer-announcer';

const row = (
  transferId: string,
  state: string,
  path = '/usb/ring.bgcode',
): PrinterTransferRow =>
  ({
    transferId,
    path,
    state,
    bytesSent: 1,
    bytesTotal: 1,
  }) as unknown as PrinterTransferRow;

describe('TransferAnnouncer', () => {
  // Opening a printer's page reads every transfer it ever had.
  it('says nothing about the transfers a printer already held', () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe(
      'printer-1',
      [row('t-1', 'done'), row('t-2', 'failed')],
      true,
    );

    expect(success).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
    success.mockRestore();
    error.mockRestore();
  });

  // The store starts empty and the listing answers a moment later, so the
  // history arrives on the second reading and not on the first.
  it('says nothing about the listing that answers after the page opened', () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe('printer-1', [], false);
    announcer.observe(
      'printer-1',
      [row('t-1', 'done'), row('t-2', 'failed')],
      true,
    );

    expect(success).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
    success.mockRestore();
    error.mockRestore();
  });

  // The store is shared, so it may hold rows from a visit before this one.
  // A transfer that ended while the person was away is history too, even if
  // it was still running when they left.
  it('says nothing about a transfer that ended before the page read the listing', () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe('printer-1', [row('t-1', 'sending')], false);
    announcer.observe('printer-1', [row('t-1', 'done')], true);

    expect(success).not.toHaveBeenCalled();
    success.mockRestore();
  });

  it('names the file once it lands, and only once', () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe('printer-1', [], true);
    announcer.observe('printer-1', [row('t-1', 'sending')], true);
    expect(success).not.toHaveBeenCalled();

    announcer.observe('printer-1', [row('t-1', 'done')], true);
    announcer.observe('printer-1', [row('t-1', 'done')], true);

    expect(success).toHaveBeenCalledTimes(1);
    expect(success).toHaveBeenCalledWith('ring.bgcode is on the printer.');
    success.mockRestore();
  });

  it('starts a fresh record for another printer', () => {
    const success = vi.spyOn(toast, 'success').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe('printer-1', [], true);
    announcer.observe('printer-2', [], false);
    announcer.observe('printer-2', [row('t-9', 'done')], true);

    expect(success).not.toHaveBeenCalled();
    success.mockRestore();
  });

  it('says a failed transfer never arrived', () => {
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe('printer-1', [], true);
    announcer.observe('printer-1', [row('t-1', 'failed')], true);

    expect(error).toHaveBeenCalledWith(
      'ring.bgcode did not reach the printer.',
    );
    error.mockRestore();
  });

  it('says why a file did not reach the printer', () => {
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const announcer = new TransferAnnouncer();

    announcer.observe('printer-1', [], true);
    announcer.observe(
      'printer-1',
      [
        {
          ...row('t-1', 'failed'),
          reason: 'Unsupported file type',
        },
      ],
      true,
    );

    expect(error).toHaveBeenCalledWith(
      'ring.bgcode did not reach the printer: Unsupported file type',
    );
    error.mockRestore();
  });

  it('says a file that was not printed, by its fault', () => {
    const error = vi.spyOn(toast, 'error').mockReturnValue('');
    const announcer = new TransferAnnouncer();
    const fault = (code: string, message: string) => ({
      code,
      status: undefined,
      params: {},
      message,
      traceId: undefined,
    });

    announcer.notPrinted('cube.bgcode', {
      answer: {
        ok: false,
        path: 'usb/cube.bgcode',
        commandId: 'c-1',
        faultCode: 'core.printer/COMMAND_REFUSED',
        machineReason: 'the printer is busy',
        transferId: undefined,
      },
      fault: fault('core.printer/COMMAND_REFUSED', 'the printer is busy'),
    });
    announcer.notPrinted('lid.bgcode', {
      fault: fault('core.unknown/NOT_IN_THE_REGISTRY', 'the printer went away'),
    });
    announcer.notPrinted('ok.bgcode', {});

    const said = error.mock.calls.map(([words]) => String(words));
    expect(said).toHaveLength(2);
    expect(said[0]).toMatch(
      /^cube\.bgcode is on the printer, but the printer did not start it: .*the printer is busy$/u,
    );
    expect(said[1]).toBe('lid.bgcode was not printed: the printer went away');
    error.mockRestore();
  });

  it('says a replace the printer refused did not reach it, with the reason', () => {
    const error = vi.spyOn(toast, 'error').mockImplementation(() => '1');
    const announcer = new TransferAnnouncer();

    announcer.notSent('cube.bgcode', {
      code: 'core.unknown/NOT_IN_THE_REGISTRY',
      status: 409,
      params: {},
      message: 'the printer still has the file open',
      traceId: undefined,
    });

    expect(String(error.mock.calls[0]?.[0])).toBe(
      'cube.bgcode did not reach the printer: the printer still has the file open',
    );
    error.mockRestore();
  });

  const refused = (params: Record<string, string> = {}) => ({
    code: 'core.unknown/NOT_IN_THE_REGISTRY',
    status: 409,
    params,
    message: 'the printer still has the file open',
    traceId: undefined,
  });

  it('says nothing about a send the person stopped', () => {
    const error = vi.spyOn(toast, 'error').mockImplementation(() => '1');
    const announcer = new TransferAnnouncer();

    announcer.notSent('cube.bgcode', refused({ event: 'stopped' }));
    announcer.notPrinted('cube.bgcode', {
      fault: refused({ event: 'stopped' }),
    });

    expect(error).not.toHaveBeenCalled();
    error.mockRestore();
  });

  // The row of a transfer that ran and failed says it, and so would the
  // answer of the send that started it.
  it('says one failure of a file once, from its row or from its answer', () => {
    const error = vi.spyOn(toast, 'error').mockImplementation(() => '1');
    const announcer = new TransferAnnouncer();
    announcer.observe('p1', [], true);

    announcer.observe(
      'p1',
      [{ ...row('t-1', 'failed'), path: 'usb/cube.bgcode', reason: 'cut off' }],
      true,
    );
    announcer.notSent('cube.bgcode', refused());

    expect(error).toHaveBeenCalledOnce();
    error.mockRestore();
  });
});
