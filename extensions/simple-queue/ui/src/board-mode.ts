/** The two ways the Print queue page draws the queues. */
export type BoardMode = 'timeline' | 'cards';

/** The key the chosen way is kept under in the person's module store. */
export const BOARD_MODE_KEY = 'board-view';

export const isBoardMode = (value: string | undefined): value is BoardMode =>
  value === 'timeline' || value === 'cards';
