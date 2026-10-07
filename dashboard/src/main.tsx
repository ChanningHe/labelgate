import '@mantine/core/styles.css';
import './styles/fonts.css';
import './styles/tokens.css';
import './global.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MantineProvider } from '@mantine/core';
import { LazyMotion, MotionConfig, domAnimation } from 'framer-motion';
import App from './App';
import { theme, cssVariablesResolver } from './theme';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} cssVariablesResolver={cssVariablesResolver} defaultColorScheme="auto">
      <LazyMotion features={domAnimation} strict>
        <MotionConfig reducedMotion="user">
          <App />
        </MotionConfig>
      </LazyMotion>
    </MantineProvider>
  </StrictMode>,
);
