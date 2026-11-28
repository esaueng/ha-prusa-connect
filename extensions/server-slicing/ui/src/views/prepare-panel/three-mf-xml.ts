export const invalid = (message: string): Error =>
  new Error(`Invalid 3MF: ${message}`);

export const children = function* (
  parent: Element,
  name: string,
): IterableIterator<Element> {
  const { namespaceURI } = parent;
  for (const element of parent.children)
    if (element.localName === name && element.namespaceURI === namespaceURI)
      yield element;
};

export const child = (parent: Element, name: string): Element | undefined => {
  for (const element of children(parent, name)) return element;
  return undefined;
};

export const idOf = (element: Element, attribute: string): number => {
  const text = element.getAttribute(attribute)?.trim() ?? '';
  const id = Number(text);
  if (!/^\d+$/u.test(text) || !Number.isSafeInteger(id))
    throw invalid(`invalid ${attribute}`);
  return id;
};
