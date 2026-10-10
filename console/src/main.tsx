import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router-dom";
import { AntdProvider } from "./app/AntdProvider";
import { AuthProvider } from "./lib/auth";
import { ConsoleQueryProvider } from "./lib/query/QueryProvider";
import { PreferencesProvider } from "./lib/preferences";
import { SiteSettingsProvider } from "./lib/siteSettings";
import { router } from "./app/router";
import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "./styles.css";
import "./app/layout.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider>
      <ConsoleQueryProvider>
        <SiteSettingsProvider>
          <PreferencesProvider>
            <AntdProvider>
              <RouterProvider router={router} />
            </AntdProvider>
          </PreferencesProvider>
        </SiteSettingsProvider>
      </ConsoleQueryProvider>
    </AuthProvider>
  </StrictMode>,
);
