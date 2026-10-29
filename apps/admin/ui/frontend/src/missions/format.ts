// Add whitespace between validated JSON tokens without reserializing numbers
// or string escapes from the saved definition.
export function formatJSON(source: string): string {
  const tokens = source.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:]+/g) ?? [];
  let depth = 0;
  let result = '';
  const newline = () => '\n' + '  '.repeat(depth);
  for (const [index, token] of tokens.entries()) {
    if (token === '{' || token === '[') {
      result += token;
      if (tokens[index + 1] !== (token === '{' ? '}' : ']')) {
        depth++;
        result += newline();
      }
    } else if (token === '}' || token === ']') {
      if (tokens[index - 1] !== (token === '}' ? '{' : '[')) {
        depth--;
        result += newline();
      }
      result += token;
    } else if (token === ',') result += ',' + newline();
    else if (token === ':') result += ': ';
    else result += token;
  }
  return result + '\n';
}
