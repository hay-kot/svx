//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#include "cocoa_darwin.h"

extern void svxItemClicked(int tag);
extern void svxMenuWillOpen(void);
extern void svxMainCallback(uintptr_t id);

@interface SvxMenuTarget : NSObject <NSMenuDelegate>
- (void)itemClicked:(NSMenuItem *)sender;
- (void)quit:(id)sender;
@end

@implementation SvxMenuTarget
- (void)itemClicked:(NSMenuItem *)sender {
	svxItemClicked((int)sender.tag);
}

- (void)quit:(id)sender {
	svxStop();
}

- (void)menuWillOpen:(NSMenu *)menu {
	svxMenuWillOpen();
}
@end

static NSStatusItem *statusItem;
static NSMenu *menu;
static SvxMenuTarget *target;

// stringWithUTF8String: returns nil on invalid UTF-8, and AppKit throws on
// a nil title.
static NSString *str(const char *s) {
	NSString *out = [NSString stringWithUTF8String:s];
	return out != nil ? out : @"?";
}

void svxInit(void) {
	[NSApplication sharedApplication];
	// Accessory: a menu bar item with no Dock icon and no app menu.
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

	target = [[SvxMenuTarget alloc] init];
	statusItem = [[[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength] retain];
	statusItem.button.imagePosition = NSImageLeft;

	menu = [[NSMenu alloc] initWithTitle:@"svx"];
	menu.autoenablesItems = NO;
	menu.delegate = target;
	statusItem.menu = menu;
}

void svxRun(void) {
	[NSApp run];
}

void svxStop(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp stop:nil];
		// stop: only ends the run loop after it handles one more event.
		NSEvent *wake = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
		                                   location:NSZeroPoint
		                              modifierFlags:0
		                                  timestamp:0
		                               windowNumber:0
		                                    context:nil
		                                    subtype:0
		                                      data1:0
		                                      data2:0];
		[NSApp postEvent:wake atStart:YES];
	});
}

void svxDispatchMain(uintptr_t id) {
	dispatch_async(dispatch_get_main_queue(), ^{
		svxMainCallback(id);
	});
}

void svxSetStatus(int running) {
	NSStatusBarButton *button = statusItem.button;
	NSString *symbol = running > 0 ? @"square.stack.3d.up.fill" : @"square.stack.3d.up";
	NSImage *image = [NSImage imageWithSystemSymbolName:symbol accessibilityDescription:@"svx"];
	[image setTemplate:YES];
	button.image = image;
	button.title = running > 0 ? [NSString stringWithFormat:@" %d", running] : @"";
	button.appearsDisabled = running == 0;
}

void svxMenuClear(void) {
	[menu removeAllItems];
}

void svxMenuAddHeader(const char *title) {
	if (@available(macOS 14.0, *)) {
		[menu addItem:[NSMenuItem sectionHeaderWithTitle:str(title)]];
		return;
	}
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:str(title) action:nil keyEquivalent:@""];
	item.enabled = NO;
	[menu addItem:item];
	[item release];
}

void svxMenuAddItem(const char *title, int tag, bool enabled) {
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:str(title)
	                                              action:(enabled ? @selector(itemClicked:) : nil)
	                                       keyEquivalent:@""];
	item.target = target;
	item.tag = tag;
	item.enabled = enabled;
	[menu addItem:item];
	[item release];
}

void svxMenuAddSeparator(void) {
	[menu addItem:[NSMenuItem separatorItem]];
}

void svxMenuAddQuit(void) {
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:@"Quit svx Menu Bar" action:@selector(quit:) keyEquivalent:@"q"];
	item.target = target;
	[menu addItem:item];
	[item release];
}
