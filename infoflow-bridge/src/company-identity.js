const label = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

export function verificationRecipient(rawEmail, allowedDomains) {
  const parts = String(rawEmail).toLowerCase().split('@');
  if (parts.length !== 2 || !/^[a-z0-9][a-z0-9._\-]*$/.test(parts[0])) return null;
  const [user, domain] = parts;
  if (!domain.endsWith('.baidu.com')) return allowedDomains.includes(domain)?user:null;
  if (!allowedDomains.includes('baidu.com')&&!allowedDomains.includes(domain)) return null;
  const child = domain.slice(0, -'.baidu.com'.length);
  return label.test(child) ? `${user}_${child}` : null;
}
