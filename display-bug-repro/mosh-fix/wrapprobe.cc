/* wrapprobe -- does an ANSI mode change clear mosh's pending-wrap flag?
   Links mosh's real terminal emulator; no network, no pty, no display. */
#include <cstdio>
#include <string>
#include "parser.h"
#include "terminal.h"
#include "terminalframebuffer.h"

using namespace Terminal;

static void feed( Emulator &emu, const std::string &s )
{
  Parser::UTF8Parser parser;
  Parser::Actions actions;
  for ( size_t i = 0; i < s.size(); i++ ) {
    actions.clear();
    parser.input( s[ i ], actions );
    for ( size_t j = 0; j < actions.size(); j++ ) {
      actions[ j ]->act_on_terminal( &emu );
    }
  }
}

static void show( const char *label, const std::string &mid )
{
  Emulator emu( 80, 25 );
  /* columns 78,79,80 -> cursor sits on the last column, wrap pending */
  feed( emu, "\033[H\033[2J\033[1;78Habc" );
  feed( emu, mid );                    /* the sequence under test */
  feed( emu, "X" );                    /* should consume the wrap -> row 2 */

  const Framebuffer &fb = emu.get_fb();
  std::string r0, r1;
  for ( int c = 77; c < 80; c++ ) r0 += fb.get_cell( 0, c )->debug_contents();
  for ( int c = 0; c < 1; c++ )   r1 += fb.get_cell( 1, c )->debug_contents();
  printf( "  %-22s row1[78..80]=%-12s row2[1]=%-8s %s\n",
          label, r0.c_str(), r1.c_str(),
          ( r0.find( 'X' ) == std::string::npos ) ? "OK (wrapped)" : "BUG (overwrote col 80)" );
}

int main( void )
{
  printf( "mosh terminal emulator, 80x25\n" );
  show( "nothing between",   "" );
  show( "ESC[4l  (RM 4)",    "\033[4l" );
  show( "ESC[4h  (SM 4)",    "\033[4h" );
  show( "ESC[?25h (DECSET)", "\033[?25h" );
  show( "ESC[0m  (SGR)",     "\033[0m" );
  return 0;
}
