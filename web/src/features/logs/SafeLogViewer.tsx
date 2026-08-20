import { useState, useRef, useEffect } from 'react';
import { Button } from '../../components/ui';

export interface LogLine {
  index: number;
  timestamp: string;
  stream: string;
  message: string;
}

interface SafeLogViewerProps {
  lines: LogLine[];
  isTruncated?: boolean;
  onRefresh?: () => void;
}

export function SafeLogViewer({ lines, isTruncated = false, onRefresh }: SafeLogViewerProps) {
  const [followTail, setFollowTail] = useState(true);
  const [filterText, setFilterText] = useState('');
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (followTail && logEndRef.current && typeof logEndRef.current.scrollIntoView === 'function') {
      logEndRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [lines, followTail]);

  const filteredLines = filterText.trim()
    ? lines.filter((l) => l.message.toLowerCase().includes(filterText.toLowerCase()) || l.stream.toLowerCase().includes(filterText.toLowerCase()))
    : lines;

  return (
    <div className="log-viewer-container" role="region" aria-label="Safe Terminal Log Viewer">
      <div className="log-toolbar">
        <div className="log-toolbar-left">
          <input
            type="text"
            className="log-filter-input"
            placeholder="Search logs…"
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
            aria-label="Search logs"
          />
          <span className="log-lines-count">{filteredLines.length} / {lines.length} lines</span>
        </div>

        <div className="log-toolbar-right">
          <Button
            variant={followTail ? 'primary' : 'secondary'}
            size="sm"
            onClick={() => setFollowTail(!followTail)}
            aria-pressed={followTail}
          >
            {followTail ? '● Following Tail' : '○ Follow Tail Paused'}
          </Button>
          {onRefresh && (
            <Button variant="ghost" size="sm" onClick={onRefresh}>
              Refresh
            </Button>
          )}
        </div>
      </div>

      {isTruncated && (
        <div className="log-truncation-notice" role="alert">
          ℹ️ Output exceeds 500 lines. Showing recent bounded buffer.
        </div>
      )}

      <div className="log-viewport" tabIndex={0}>
        {filteredLines.length === 0 ? (
          <div className="log-empty">No log lines match filter.</div>
        ) : (
          filteredLines.map((line) => (
            <div key={line.index} className={`log-line log-stream-${line.stream}`}>
              <span className="log-num">{line.index}</span>
              <span className="log-time font-mono">{new Date(line.timestamp).toLocaleTimeString()}</span>
              <span className={`log-stream-badge stream-${line.stream}`}>[{line.stream.toUpperCase()}]</span>
              <span className="log-msg font-mono">{line.message}</span>
            </div>
          ))
        )}
        <div ref={logEndRef} />
      </div>
    </div>
  );
}
