import { useState, useEffect, useCallback, useRef } from 'react';
import { api } from '../../api/client';
import { StatusBadge } from '../../components/ui';

interface SearchResultItem {
  entity_type: string;
  id: string;
  title: string;
  subtitle: string;
  route_target: string;
  badge_status: string;
  score: number;
}

interface GlobalEntityNavigatorProps {
  isOpen: boolean;
  onClose: () => void;
  onNavigate: (route: string) => void;
}

export function GlobalEntityNavigator({ isOpen, onClose, onNavigate }: GlobalEntityNavigatorProps) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<SearchResultItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(0);

  const inputRef = useRef<HTMLInputElement>(null);

  const performSearch = useCallback(async (q: string) => {
    if (!q.trim()) {
      setResults([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const resp = await api.searchGlobal(q);
      setResults(resp.results);
      setSelectedIndex(0);
    } catch {
      setResults([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setResults([]);
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  useEffect(() => {
    const handler = setTimeout(() => {
      void performSearch(query);
    }, 150);
    return () => clearTimeout(handler);
  }, [query, performSearch]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((prev) => (results.length > 0 ? (prev + 1) % results.length : 0));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((prev) => (results.length > 0 ? (prev - 1 + results.length) % results.length : 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (results[selectedIndex]) {
        handleSelect(results[selectedIndex]);
      }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    }
  };

  const handleSelect = (item: SearchResultItem) => {
    onClose();
    const route = item.route_target.replace(/^\//, '').split('/')[0];
    onNavigate(route || 'overview');
  };

  if (!isOpen) return null;

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Global Navigator">
      <div className="modal-card modal-lg" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '640px' }}>
        <div className="search-navigator-header">
          <input
            ref={inputRef}
            type="text"
            className="form-input search-navigator-input text-sm"
            placeholder="Search tasks, runs, agents, memories, evidence, or exact IDs (e.g. TSK-001)…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={handleKeyDown}
          />
        </div>

        <div className="search-navigator-body">
          {loading ? (
            <div className="p-4 text-center text-xs text-dim">Searching catalog…</div>
          ) : results.length > 0 ? (
            <div className="search-results-list" role="listbox">
              {results.map((item, idx) => (
                <div
                  key={`${item.entity_type}-${item.id}`}
                  role="option"
                  aria-selected={idx === selectedIndex}
                  className={`search-result-item ${idx === selectedIndex ? 'selected' : ''}`}
                  onClick={() => handleSelect(item)}
                  onMouseEnter={() => setSelectedIndex(idx)}
                >
                  <div className="flex-row items-center justify-between" style={{ marginBottom: 'var(--space-1)' }}>
                    <div className="flex-row items-center gap-2">
                      <span className="diff-tag tag-modified text-xs">{item.entity_type.toUpperCase()}</span>
                      <code className="font-mono text-xs font-bold">{item.id}</code>
                    </div>
                    <StatusBadge status="ready" label={item.badge_status} />
                  </div>
                  <div className="text-xs font-semibold">{item.title}</div>
                  <div className="text-xs text-dim">{item.subtitle}</div>
                </div>
              ))}
            </div>
          ) : query ? (
            <div className="p-4 text-center text-xs text-dim">No matching entities found for "{query}"</div>
          ) : (
            <div className="p-4 text-center text-xs text-dim">
              Type an ID or title keyword to search across the entire control plane.
            </div>
          )}
        </div>

        <div className="search-navigator-footer flex-row items-center justify-between text-xs text-dim">
          <span>Navigate: <strong>↑</strong> <strong>↓</strong> | Select: <strong>Enter</strong> | Close: <strong>Esc</strong></span>
          <span>Zero Client Dump Invariant</span>
        </div>
      </div>
    </div>
  );
}
