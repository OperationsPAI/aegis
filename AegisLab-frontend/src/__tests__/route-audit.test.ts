/**
 * Route Audit Test
 *
 * Static analysis test that reads source files as strings and verifies
 * that all navigate() and <Link to="..."> targets match a defined route
 * in App.tsx. Does NOT render React components.
 */
import { readdirSync, readFileSync, statSync } from 'fs';
import { join, relative } from 'path';
import { describe, expect, it } from 'vitest';

const ROOT = join(__dirname, '..');
const PAGES_DIR = join(ROOT, 'pages');
const APP_TSX = join(ROOT, 'App.tsx');

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Recursively collect all .tsx files under a directory */
function collectTsxFiles(dir: string): string[] {
  const results: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) {
      results.push(...collectTsxFiles(full));
    } else if (full.endsWith('.tsx')) {
      results.push(full);
    }
  }
  return results;
}

/** Extract route path definitions from App.tsx */
function extractRoutePaths(source: string): string[] {
  const paths: string[] = [];
  // Match path='...' or path="..."
  const regex = /path=['"]([^'"]+)['"]/g;
  let m: RegExpExecArray | null;
  while ((m = regex.exec(source)) !== null) {
    let p = m[1];
    // Routes in App.tsx are relative (no leading /); normalize to absolute
    if (!p.startsWith('/')) {
      p = `/${p}`;
    }
    paths.push(p);
  }
  return paths;
}

/** Extract static navigate targets from source code */
function extractNavigateTargets(source: string): string[] {
  const targets: string[] = [];

  // navigate('...'), navigate("...") — skip template literals with ${
  const navRegex = /navigate\(\s*['"`]([^'"`$]+)['"`]\s*\)/g;
  let m: RegExpExecArray | null;
  while ((m = navRegex.exec(source)) !== null) {
    const target = m[1];
    // Skip relative numeric navigation like navigate(-1)
    if (/^-?\d+$/.test(target)) continue;
    targets.push(target);
  }

  // <Link to='...' or <Link to="..."
  const linkRegex = /<Link\s[^>]*to=['"]([^'"$]+)['"]/g;
  while ((m = linkRegex.exec(source)) !== null) {
    targets.push(m[1]);
  }

  return targets;
}

/**
 * Convert a route pattern like /projects/:id/settings into a regex
 * that matches concrete paths like /projects/42/settings.
 */
function routePatternToRegex(pattern: string): RegExp {
  // Escape special regex chars except : which we handle
  const escaped = pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Replace :param segments with [^/]+
  const withParams = escaped.replace(/:[\w]+/g, '[^/]+');
  return new RegExp(`^${withParams}$`);
}

/** Check if a concrete path matches any of the route patterns */
function matchesAnyRoute(target: string, routePatterns: RegExp[]): boolean {
  return routePatterns.some((re) => re.test(target));
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('Route Audit', () => {
  const appSource = readFileSync(APP_TSX, 'utf-8');
  const routePaths = extractRoutePaths(appSource);
  const routeRegexes = routePaths.map(routePatternToRegex);

  // Also add well-known special routes that are valid but not explicit <Route>
  // entries (e.g. / redirects to /home, /login is outside layout)
  const specialPaths = ['/', '/login', '/home'];
  const allRegexes = [
    ...routeRegexes,
    ...specialPaths.map(routePatternToRegex),
  ];

  const pageFiles = collectTsxFiles(PAGES_DIR);

  // Also scan components directory for navigate/Link usage
  const componentsDir = join(ROOT, 'components');
  let componentFiles: string[] = [];
  try {
    componentFiles = collectTsxFiles(componentsDir);
  } catch {
    // components dir may not exist
  }

  const allFiles = [...pageFiles, ...componentFiles];

  // Collect all targets with their source locations
  const allTargets: Array<{ file: string; target: string }> = [];

  for (const file of allFiles) {
    const source = readFileSync(file, 'utf-8');
    const targets = extractNavigateTargets(source);
    for (const target of targets) {
      allTargets.push({ file: relative(ROOT, file), target });
    }
  }

  it('should have route definitions extracted from App.tsx', () => {
    expect(routePaths.length).toBeGreaterThan(0);
    // Verify key routes exist
    expect(routePaths).toContain('/home');
    expect(routePaths).toContain('/projects');
    expect(routePaths).toContain('/projects/:id');
    expect(routePaths).toContain('/admin/containers');
    expect(routePaths).toContain('/admin/datasets');
  });

  it('every static navigate/Link target should match a defined route', () => {
    const unmatched: Array<{ file: string; target: string }> = [];

    for (const { file, target } of allTargets) {
      // Skip dynamic targets (containing template expressions or variables)
      if (target.includes('${') || target.includes('$')) continue;
      // Skip hash-only or query-only targets
      if (target.startsWith('#') || target.startsWith('?')) continue;
      // Skip relative paths (., ..)
      if (target.startsWith('.')) continue;

      if (!matchesAnyRoute(target, allRegexes)) {
        unmatched.push({ file, target });
      }
    }

    if (unmatched.length > 0) {
      const details = unmatched
        .map(({ file, target }) => `  ${file} -> ${target}`)
        .join('\n');
      expect.fail(
        `Found ${unmatched.length} navigation target(s) with no matching route:\n${details}`
      );
    }
  });

  it('should not navigate to container routes without /admin/ prefix', () => {
    const bad = allTargets.filter(({ target }) => {
      // Matches /containers/... but NOT /admin/containers/...
      return (
        /^\/containers(\/|$)/.test(target) &&
        !target.startsWith('/admin/containers')
      );
    });

    if (bad.length > 0) {
      const details = bad
        .map(({ file, target }) => `  ${file} -> ${target}`)
        .join('\n');
      expect.fail(
        `Found container route(s) missing /admin/ prefix:\n${details}`
      );
    }
  });

  it('should not navigate to dataset routes without /admin/ prefix', () => {
    const bad = allTargets.filter(({ target }) => {
      return (
        /^\/datasets(\/|$)/.test(target) &&
        !target.startsWith('/admin/datasets')
      );
    });

    if (bad.length > 0) {
      const details = bad
        .map(({ file, target }) => `  ${file} -> ${target}`)
        .join('\n');
      expect.fail(
        `Found dataset route(s) missing /admin/ prefix:\n${details}`
      );
    }
  });

  it('should not navigate to /projects/new (use modal instead)', () => {
    const bad = allTargets.filter(({ target }) => target === '/projects/new');

    if (bad.length > 0) {
      const details = bad
        .map(({ file, target }) => `  ${file} -> ${target}`)
        .join('\n');
      expect.fail(
        `Found navigation to /projects/new (should use modal):\n${details}`
      );
    }
  });

  it('should not navigate to /projects/:id/upload (removed route)', () => {
    const bad = allTargets.filter(({ target }) =>
      /^\/projects\/[^/]+\/upload$/.test(target)
    );

    if (bad.length > 0) {
      const details = bad
        .map(({ file, target }) => `  ${file} -> ${target}`)
        .join('\n');
      expect.fail(
        `Found navigation to removed /projects/:id/upload route:\n${details}`
      );
    }
  });
});
