interface CodeBlockProps {
  children: string;
  language?: string;
}

export function CodeBlock({ children, language }: CodeBlockProps) {
  return (
    <pre className="code-block" data-language={language}>
      <code>{children}</code>
    </pre>
  );
}
