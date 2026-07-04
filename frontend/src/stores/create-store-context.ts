// Factory for the repeated "createContext + useContext with undefined guard"
// pattern shared by board-context and chat-context.
import { createContext, useContext } from 'react';

interface StoreContext<T> {
  Context: React.Context<T | undefined>;
  useStore: () => T;
}

export function createStoreContext<T>(name: string): StoreContext<T> {
  const Context = createContext<T | undefined>(undefined);
  function useStore(): T {
    const value = useContext(Context);
    if (value === undefined) {
      throw new Error(`${name} must be used within its Provider`);
    }
    return value;
  }
  return { Context, useStore };
}
