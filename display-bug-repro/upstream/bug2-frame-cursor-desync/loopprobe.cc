/* loopprobe -- mosh syncs state by shipping what Display::new_frame emits and
   replaying it into the client's own Emulator. So the round trip
        state A --new_frame--> bytes --mosh Emulator--> state B
   must land exactly on the target. This runs that loop, in process, with no
   network, and reports the first state where it does not. */
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <sstream>
#include <string>
#include <vector>
#include "parser.h"
#include "terminal.h"
#include "terminaldisplay.h"
#include "terminalframebuffer.h"

using namespace Terminal;

static std::string row_text( const Framebuffer &fb, int row )
{
  std::string s;
  for ( int c = 0; c < fb.ds.get_width(); c++ ) {
    const Cell *cell = fb.get_cell( row, c );
    if ( cell->is_blank() ) { s += ' '; continue; }
    cell->print_grapheme( s );
  }
  while ( !s.empty() && s[ s.size() - 1 ] == ' ' ) s.erase( s.size() - 1 );
  return s;
}

static void feed( Emulator &e, const std::string &s )
{
  Parser::UTF8Parser p; Parser::Actions a;
  for ( size_t i = 0; i < s.size(); i++ ) {
    a.clear(); p.input( s[ i ], a );
    for ( size_t j = 0; j < a.size(); j++ ) a[ j ]->act_on_terminal( &e );
  }
}

int main( int argc, char **argv )
{
  std::ifstream f( argv[ 1 ], std::ios::binary );
  std::stringstream ss; ss << f.rdbuf(); std::string data = ss.str();
  std::vector<size_t> sizes;
  { std::ifstream w( argv[ 2 ] ); std::string line;
    while ( std::getline( w, line ) ) {
      size_t c = line.find( ',' );
      if ( c != std::string::npos ) sizes.push_back( atoi( line.substr( c + 1 ).c_str() ) );
    } }

  Emulator server( 80, 25 );      /* the server's terminal */
  Emulator client( 80, 25 );      /* the client's reconstruction */
  Display display( false );
  Framebuffer last = server.get_fb();
  bool initialized = false;
  size_t off = 0;

  for ( size_t k = 0; k < sizes.size(); k++ ) {
    std::string chunk = data.substr( off, sizes[ k ] );
    off += sizes[ k ];
    feed( server, chunk );

    std::string update = display.new_frame( initialized, last, server.get_fb() );
    initialized = true;
    feed( client, update );
    last = server.get_fb();

    std::string want = row_text( server.get_fb(), 24 );
    std::string got  = row_text( client.get_fb(), 24 );
    printf( "state %zu: server %2zu (cur %d,%d wrap=%d)  client %2zu (cur %d,%d wrap=%d)  %s\n",
            k + 1, want.size(),
            server.get_fb().ds.get_cursor_row(), server.get_fb().ds.get_cursor_col(),
            (int)server.get_fb().ds.next_print_will_wrap,
            got.size(),
            client.get_fb().ds.get_cursor_row(), client.get_fb().ds.get_cursor_col(),
            (int)client.get_fb().ds.next_print_will_wrap,
            want == got ? "ok" : "*** DIVERGED ***" );
    if ( want != got ) {
      printf( "   server: %s\n   client: %s\n", want.c_str(), got.c_str() );
      printf( "   the %zu bytes new_frame emitted:\n     ", update.size() );
      for ( size_t z = 0; z < update.size(); z++ ) {
        unsigned char c = update[ z ];
        if ( c == 0x1b ) printf( "<ESC>" );
        else if ( c == '\r' ) printf( "<CR>" );
        else if ( c == '\n' ) printf( "<LF>" );
        else if ( c == 0x08 ) printf( "<BS>" );
        else if ( c < 0x20 || c > 0x7e ) printf( "<%02x>", c );
        else putchar( c );
      }
      printf( "\n" );
      printf( "   source state row (%zu): %s\n", row_text( last, 24 ).size(),
              row_text( last, 24 ).c_str() );
      return 1;
    }
  }
  printf( "round trip clean\n" );
  return 0;
}
