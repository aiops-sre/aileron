import React from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark, oneLight } from 'react-syntax-highlighter/dist/esm/styles/prism';

interface MarkdownRendererProps {
  content: string;
  isDark?: boolean;
}

export function MarkdownRenderer({ content, isDark = false }: MarkdownRendererProps) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        code({ inline, className, children, ...props }: any) {
          const match = /language-(\w+)/.exec(className || '');
          const language = match ? match[1] : '';

          if (!inline && language) {
            return (
              <div style={{ margin: '12px 0' }}>
                <div style={{
                  background: 'var(--color-separator)',
                  padding: '8px 12px',
                  fontSize: '12px',
                  fontWeight: 600,
                  color: 'var(--color-text-secondary)',
                  borderTopLeftRadius: '8px',
                  borderTopRightRadius: '8px',
                  textTransform: 'uppercase',
                  letterSpacing: '0.5px'
                }}>
                  {language}
                </div>
                <SyntaxHighlighter
                  style={isDark ? oneDark : oneLight}
                  language={language}
                  PreTag="div"
                  customStyle={{
                    margin: 0,
                    borderTopLeftRadius: 0,
                    borderTopRightRadius: 0,
                    borderBottomLeftRadius: '8px',
                    borderBottomRightRadius: '8px',
                    fontSize: '13px',
                    lineHeight: '1.5'
                  }}
                  {...props}
                >
                  {String(children).replace(/\n$/, '')}
                </SyntaxHighlighter>
              </div>
            );
          }

          return (
            <code
              style={{
                background: 'rgba(142, 142, 147, 0.1)',
                padding: '2px 6px',
                borderRadius: '4px',
                fontSize: '0.9em',
                fontFamily: 'SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace'
              }}
              {...props}
            >
              {children}
            </code>
          );
        },
        pre({ children }) {
          return <div>{children}</div>;
        },
        blockquote({ children }) {
          return (
            <blockquote style={{
              borderLeft: '4px solid var(--color-blue)',
              paddingLeft: '16px',
              margin: '16px 0',
              fontStyle: 'italic',
              color: 'var(--color-text-secondary)',
              background: 'rgba(0, 113, 227, 0.05)',
              padding: '12px 16px',
              borderRadius: '8px'
            }}>
              {children}
            </blockquote>
          );
        },
        table({ children }) {
          return (
            <div style={{ overflowX: 'auto', margin: '16px 0' }}>
              <table style={{
                width: '100%',
                borderCollapse: 'collapse',
                border: '1px solid var(--color-separator)',
                borderRadius: '8px',
                overflow: 'hidden'
              }}>
                {children}
              </table>
            </div>
          );
        },
        th({ children }) {
          return (
            <th style={{
              background: 'var(--color-fill)',
              padding: '12px',
              textAlign: 'left',
              borderBottom: '1px solid var(--color-separator)',
              fontWeight: 600
            }}>
              {children}
            </th>
          );
        },
        td({ children }) {
          return (
            <td style={{
              padding: '12px',
              borderBottom: '1px solid var(--color-separator)'
            }}>
              {children}
            </td>
          );
        },
        ul({ children }) {
          return (
            <ul style={{
              paddingLeft: '24px',
              margin: '8px 0'
            }}>
              {children}
            </ul>
          );
        },
        ol({ children }) {
          return (
            <ol style={{
              paddingLeft: '24px',
              margin: '8px 0'
            }}>
              {children}
            </ol>
          );
        },
        h1({ children }) {
          return (
            <h1 style={{
              fontSize: '24px',
              fontWeight: 600,
              margin: '24px 0 16px 0',
              borderBottom: '2px solid var(--color-separator)',
              paddingBottom: '8px'
            }}>
              {children}
            </h1>
          );
        },
        h2({ children }) {
          return (
            <h2 style={{
              fontSize: '20px',
              fontWeight: 600,
              margin: '20px 0 12px 0',
              color: 'var(--color-text)'
            }}>
              {children}
            </h2>
          );
        },
        h3({ children }) {
          return (
            <h3 style={{
              fontSize: '18px',
              fontWeight: 600,
              margin: '16px 0 8px 0',
              color: 'var(--color-text)'
            }}>
              {children}
            </h3>
          );
        },
        a({ href, children }) {
          return (
            <a
              href={href}
              target="_blank"
              rel="noopener noreferrer"
              style={{
                color: 'var(--color-blue)',
                textDecoration: 'none',
                borderBottom: '1px solid transparent',
                transition: 'border-color 0.2s'
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.borderBottomColor = 'var(--color-blue)';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.borderBottomColor = 'transparent';
              }}
            >
              {children}
            </a>
          );
        }
      }}
    >
      {content}
    </ReactMarkdown>
  );
}