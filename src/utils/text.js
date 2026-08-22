export const extractPlainText = (text) => {
  return text
    .replace(/<\/?(p|div|h[1-6]|li|blockquote|br)[^>]*>/gi, '\n')
    .replace(/<[^>]+>/g, '')
    .replace(/!\[.*?\]\(.*?\)/g, '')
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/^[# \t\-+>]+/gm, '')
    .replace(/[*_~`]/g, '')
    .trim()
}

export const extractNoteTitle = (html, fallback = '') => {
  const source = String(html || '')
  if (typeof document !== 'undefined') {
    const container = document.createElement('div')
    container.innerHTML = source
    const titleNode = container.querySelector('[data-note-title], h1, h2, h3')
    const title = titleNode?.textContent?.replace(/\s+/g, ' ').trim()
    if (title) return title.substring(0, 50)
  }
  const plainText = extractPlainText(source)
  const firstLine = plainText.split('\n').find(line => line.trim())?.trim()
  return (firstLine || fallback || '').substring(0, 50)
}
