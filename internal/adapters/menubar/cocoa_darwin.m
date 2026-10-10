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
// menuStack is the chain of menus being filled, the root first; new items go to
// the last. svx nests three deep: stack, service, actions.
enum { maxDepth = 8 };
static NSMenu *menuStack[maxDepth];
static int depth;
#define current (menuStack[depth])
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
	menuStack[0] = menu;
	depth = 0;
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
	depth = 0;
}

void svxMenuAddHeader(const char *title) {
	if (@available(macOS 14.0, *)) {
		[current addItem:[NSMenuItem sectionHeaderWithTitle:str(title)]];
		return;
	}
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:str(title) action:nil keyEquivalent:@""];
	item.enabled = NO;
	[current addItem:item];
	[item release];
}

enum { toneNormal, toneOK, toneBad, toneDim };

// detailGap is the least space between a row's title and its detail.
static const CGFloat detailGap = 24;

// rowTitle draws "● name<TAB>detail": an optional health dot, the title,
// and a monospaced-digit detail in the secondary colour. svxMenuFinish sets
// the tab stop that right-aligns every detail once all rows exist.
//
// Indented rows use a paragraph indent the width of the dot rather than
// indentationLevel: a tab stop is measured from the row's origin, which
// indentationLevel moves and a paragraph indent does not, so all rows keep
// one origin and their details line up.
static NSAttributedString *rowTitle(const char *title, const char *detail, int tone, bool bold, int indent) {
	CGFloat size = [NSFont systemFontSize];
	NSFont *font = bold ? [NSFont boldSystemFontOfSize:size] : [NSFont menuFontOfSize:size];
	NSColor *fg = [NSColor labelColor];
	if (tone == toneBad && !bold) {
		fg = [NSColor systemRedColor];
	} else if (tone == toneDim) {
		fg = [NSColor secondaryLabelColor];
	}

	NSMutableAttributedString *out = [[NSMutableAttributedString alloc] init];
	if (tone == toneOK || (tone == toneBad && bold)) {
		NSColor *dot = tone == toneOK ? [NSColor systemGreenColor] : [NSColor systemRedColor];
		NSAttributedString *d = [[NSAttributedString alloc] initWithString:@"● "
			attributes:@{NSFontAttributeName : [NSFont menuFontOfSize:size * 0.8], NSForegroundColorAttributeName : dot}];
		[out appendAttributedString:d];
		[d release];
	}
	NSAttributedString *t = [[NSAttributedString alloc] initWithString:str(title)
		attributes:@{NSFontAttributeName : font, NSForegroundColorAttributeName : fg}];
	[out appendAttributedString:t];
	[t release];
	if (detail != NULL && detail[0] != '\0') {
		NSColor *dc = tone == toneBad && !bold ? [NSColor systemRedColor] : [NSColor secondaryLabelColor];
		NSString *text = [@"\t" stringByAppendingString:str(detail)];
		NSAttributedString *d = [[NSAttributedString alloc] initWithString:text
			attributes:@{NSFontAttributeName : [NSFont monospacedDigitSystemFontOfSize:size weight:NSFontWeightRegular], NSForegroundColorAttributeName : dc}];
		[out appendAttributedString:d];
		[d release];
	}
	if (indent > 0) {
		CGFloat dot = [@"● " sizeWithAttributes:@{NSFontAttributeName : [NSFont menuFontOfSize:size * 0.8]}].width;
		NSMutableParagraphStyle *para = [[NSMutableParagraphStyle alloc] init];
		para.firstLineHeadIndent = dot * indent;
		para.headIndent = dot * indent;
		[out addAttribute:NSParagraphStyleAttributeName value:para range:NSMakeRange(0, out.length)];
		[para release];
	}
	return [out autorelease];
}

static NSParagraphStyle *paraOf(NSAttributedString *t) {
	return t.length > 0 ? [t attribute:NSParagraphStyleAttributeName atIndex:0 effectiveRange:NULL] : nil;
}

// svxMenuFinish right-aligns every detail in the top-level menu at one tab
// stop: just past the widest title, plus the widest detail.
void svxMenuFinish(void) {
	CGFloat titleWidth = 0, detailWidth = 0;
	for (NSMenuItem *item in menu.itemArray) {
		NSAttributedString *t = item.attributedTitle;
		NSRange tab = [t.string rangeOfString:@"\t"];
		if (t == nil || tab.location == NSNotFound) {
			continue;
		}
		CGFloat lead = paraOf(t).firstLineHeadIndent;
		titleWidth = MAX(titleWidth, lead + [t attributedSubstringFromRange:NSMakeRange(0, tab.location)].size.width);
		NSRange rest = NSMakeRange(tab.location + 1, t.length - tab.location - 1);
		detailWidth = MAX(detailWidth, [t attributedSubstringFromRange:rest].size.width);
	}
	if (detailWidth == 0) {
		return;
	}

	NSTextTab *stop = [[NSTextTab alloc] initWithTextAlignment:NSTextAlignmentRight
	                                                  location:ceil(titleWidth + detailGap + detailWidth)
	                                                   options:@{}];
	for (NSMenuItem *item in menu.itemArray) {
		if (item.attributedTitle == nil || [item.attributedTitle.string rangeOfString:@"\t"].location == NSNotFound) {
			continue;
		}
		NSMutableAttributedString *t = [item.attributedTitle mutableCopy];
		NSParagraphStyle *old = paraOf(t);
		NSMutableParagraphStyle *para = old != nil ? [old mutableCopy] : [[NSMutableParagraphStyle alloc] init];
		para.tabStops = @[ stop ];
		[t addAttribute:NSParagraphStyleAttributeName value:para range:NSMakeRange(0, t.length)];
		item.attributedTitle = t;
		[para release];
		[t release];
	}
	[stop release];
}

void svxMenuAddItem(const char *title, const char *detail, int tag, bool enabled, int indent, int tone, bool alternate) {
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:str(title)
	                                              action:(enabled ? @selector(itemClicked:) : nil)
	                                       keyEquivalent:@""];
	item.attributedTitle = rowTitle(title, detail, tone, false, indent);
	item.target = target;
	item.tag = tag;
	item.enabled = enabled;
	if (alternate) {
		// An alternate replaces the item above it while its modifier is
		// held. The pair must differ only in the modifier mask.
		item.keyEquivalentModifierMask = NSEventModifierFlagOption;
		item.alternate = YES;
	}
	[current addItem:item];
	[item release];
}

void svxMenuAddSeparator(void) {
	[current addItem:[NSMenuItem separatorItem]];
}

void svxMenuBeginSubmenu(const char *title, const char *detail, int tone, bool bold) {
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:str(title) action:nil keyEquivalent:@""];
	item.attributedTitle = rowTitle(title, detail, tone, bold, 0);
	NSMenu *sub = [[NSMenu alloc] initWithTitle:str(title)];
	sub.autoenablesItems = NO;
	item.submenu = sub;
	[current addItem:item];
	if (depth < maxDepth - 1) {
		menuStack[++depth] = sub;
	}
	[sub release];
	[item release];
}

void svxMenuEndSubmenu(void) {
	if (depth > 0) {
		depth--;
	}
}

void svxMenuAddQuit(void) {
	// No ⌘Q: any key equivalent makes AppKit reserve a shortcut column on
	// every row of the menu.
	NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:@"Quit svx Menu Bar" action:@selector(quit:) keyEquivalent:@""];
	item.target = target;
	[menu addItem:item];
	[item release];
}
