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
// tone is 0 normal, 1 ok, 2 bad, 3 dim, matching the Go Tone values.
void svxMenuAddItem(const char *title, const char *detail, int tag, bool enabled, int indent, int tone, bool alternate);
void svxMenuAddSeparator(void);
void svxMenuBeginSubmenu(const char *title, const char *detail, int tone, bool bold);
void svxMenuEndSubmenu(void);
void svxMenuAddQuit(void);
void svxMenuFinish(void);
