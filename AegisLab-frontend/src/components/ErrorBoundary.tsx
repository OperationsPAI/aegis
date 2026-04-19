import React from 'react';

import { Button, Result, Typography } from 'antd';

const { Paragraph, Text } = Typography;

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
  showDetails: boolean;
}

interface ErrorBoundaryProps {
  children: React.ReactNode;
}

class ErrorBoundary extends React.Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, error: null, showDetails: false };
  }

  static getDerivedStateFromError(error: Error): Partial<ErrorBoundaryState> {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo): void {
    console.error('ErrorBoundary caught an error:', error, errorInfo);
  }

  handleReload = () => {
    window.location.reload();
  };

  handleToggleDetails = () => {
    this.setState((prev) => ({ showDetails: !prev.showDetails }));
  };

  render() {
    if (this.state.hasError) {
      return (
        <div
          style={{
            display: 'flex',
            justifyContent: 'center',
            alignItems: 'center',
            minHeight: '100vh',
            padding: 24,
          }}
        >
          <Result
            status='error'
            title='Something went wrong'
            subTitle='An unexpected error occurred. Please try reloading the page.'
            extra={[
              <Button type='primary' key='reload' onClick={this.handleReload}>
                Reload
              </Button>,
              <Button key='home' href='/'>
                Go Home
              </Button>,
              <Button
                type='link'
                key='details'
                onClick={this.handleToggleDetails}
              >
                {this.state.showDetails ? 'Hide Details' : 'Show Details'}
              </Button>,
            ]}
          >
            {this.state.showDetails && this.state.error && (
              <Paragraph>
                <Text type='danger' code>
                  {this.state.error.message}
                </Text>
              </Paragraph>
            )}
          </Result>
        </div>
      );
    }

    return this.props.children;
  }
}

export default ErrorBoundary;
