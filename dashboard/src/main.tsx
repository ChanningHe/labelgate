import '@mantine/core/styles.css';
import './global.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MantineProvider, createTheme } from '@mantine/core';
import { LazyMotion, MotionConfig, domAnimation } from 'framer-motion';
import App from './App';

const theme = createTheme({
  primaryColor: 'orange',
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
  defaultRadius: 'md',
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="auto">
      <LazyMotion features={domAnimation} strict>
        <MotionConfig reducedMotion="user">
          <App />
        </MotionConfig>
      </LazyMotion>
    </MantineProvider>
  </StrictMode>,
);
