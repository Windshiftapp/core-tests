import { describe, expect, it } from 'vitest';
import { buildTimeReportMarkdown, escapeMarkdownText } from './timeReportMarkdown.js';

describe('time report Markdown', () => {
  it('escapes values that could change report structure', () => {
    const markdown = buildTimeReportMarkdown({
      title: 'Project #1 [link](javascript:alert(1))',
      period: { from: '2026-08-01', to: '2026-08-31' },
      generated: '2026-08-12',
      summary: [{ label: 'Top Project', value: '`Launch`\n- injected list' }],
      team: [
        {
          name: 'Ada | ![pixel](https://example.com/pixel.png)',
          hours: '8h',
          entries: 2,
          average: '4h',
        },
      ],
      entries: [
        {
          heading: '# injected heading',
          fields: [
            {
              label: 'Description',
              value: '<script>alert(1)</script>\n[click](javascript:alert(1))',
            },
          ],
        },
      ],
    });

    expect(markdown).toContain(String.raw`# Project \#1 \[link\]\(javascript\:alert\(1\)\)`);
    expect(markdown).toContain(String.raw`| Ada \| \!\[pixel\]\(https\:`);
    expect(markdown).toContain('\u200b');
    expect(markdown).toContain(String.raw`### \# injected heading`);
    expect(markdown).toContain(
      String.raw`\<script\>alert\(1\)\<\/script\> \[click\]\(javascript\:alert\(1\)\)`
    );
    expect(markdown).not.toContain('![pixel](');
    expect(markdown).not.toContain('<script>');
    expect(markdown).not.toContain('\n# injected heading');
  });

  it('renders the shared summary, team table, entry, and total sections', () => {
    const markdown = buildTimeReportMarkdown({
      title: 'Time Tracking Report',
      period: { from: 'All time', to: 'Present' },
      generated: '2026-08-12',
      summary: [{ label: 'Total Hours', value: '3.5h' }],
      team: [{ name: 'Sam', hours: '3.5h', entries: 2, average: '1.75h' }],
      entries: [
        {
          heading: '2026-08-12 — Project',
          fields: [{ label: 'Description', value: 'Planning' }],
        },
      ],
      totalSummary: 'Grand Total: 3.5 hours across 2 entries.',
    });

    expect(markdown).toContain('## Summary');
    expect(markdown).toContain('## Team Breakdown');
    expect(markdown).toContain('| Member | Hours | Entries | Avg/Day |');
    expect(markdown).toContain('## Time Entries');
    expect(markdown).toContain('## Total Summary');
    expect(markdown).toContain(String.raw`Grand Total\: 3\.5 hours across 2 entries\.`);
  });
});

describe('escapeMarkdownText', () => {
  it('collapses newlines and escapes Markdown punctuation', () => {
    expect(escapeMarkdownText('line 1\n> quote | `code`')).toBe('line 1 \\> quote \\| \\`code\\`');
  });
});
