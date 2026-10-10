//go:build darwin && cgo

#include <stdbool.h>
#include <stdint.h>

void svxInit(void);
void svxRun(void);
void svxStop(void);
void svxDispatchMain(uintptr_t id);

// The functions below touch AppKit and must run on the main thread.
void svxSetStatus(int running);
void svxMenuClear(void);
void svxMenuAddHeader(const char *title);
void svxMenuAddItem(const char *title, int tag, bool enabled);
void svxMenuAddSeparator(void);
void svxMenuAddQuit(void);
